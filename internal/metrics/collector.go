package metrics

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/codeswhat/portwing/internal/docker"
)

// HostMetrics contains system-level resource metrics.
type HostMetrics struct {
	CPUUsage    float64 `json:"cpuUsage"`
	CPUCores    int     `json:"cpuCores"`
	MemoryTotal uint64  `json:"memoryTotal"`
	MemoryUsed  uint64  `json:"memoryUsed"`
	MemoryFree  uint64  `json:"memoryFree"`
	DiskTotal   uint64  `json:"diskTotal"`
	DiskUsed    uint64  `json:"diskUsed"`
	DiskFree    uint64  `json:"diskFree"`
	// DiskMetricsAvailable reports whether the three counts above were actually
	// measured. False leaves them at zero, which a consumer must not read as an
	// empty filesystem. DiskError says why the measurement failed, and stays
	// empty when disk collection was never attempted (SKIP_DF_COLLECTION).
	DiskMetricsAvailable bool   `json:"diskMetricsAvailable"`
	DiskError            string `json:"diskError,omitempty"`
	NetworkRxBytes       uint64 `json:"networkRxBytes"`
	NetworkTxBytes       uint64 `json:"networkTxBytes"`
	Uptime               uint64 `json:"uptime"`
}

type cpuStats struct {
	user    uint64
	nice    uint64
	system  uint64
	idle    uint64
	iowait  uint64
	irq     uint64
	softirq uint64
	steal   uint64
}

func (s *cpuStats) total() uint64 {
	return s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq + s.steal
}

func (s *cpuStats) idleTotal() uint64 {
	return s.idle + s.iowait
}

// DockerInfoClient is the Docker API subset needed to learn where the daemon
// actually keeps its data.
type DockerInfoClient interface {
	GetDockerInfo(ctx context.Context) (*docker.DockerInfo, error)
}

// defaultDockerDataRoot is Docker's upstream default data directory, used as
// the fallback whenever the daemon cannot say where its own data lives.
const defaultDockerDataRoot = "/var/lib/docker"

const (
	// dataRootTimeout bounds the /info lookup so a wedged Docker socket cannot
	// stall a Prometheus scrape or the edge metrics tick.
	dataRootTimeout = 2 * time.Second
	// dataRootRetryInterval keeps a daemon that is still starting from costing
	// a full dataRootTimeout on every subsequent collection.
	dataRootRetryInterval = 30 * time.Second
)

// Collector gathers host-level system metrics.
type Collector struct {
	mu             sync.Mutex
	dockerDataRoot string
	skipDisk       bool
	prevCPU        *cpuStats
	// procRoot overrides the /proc filesystem root used for reading system
	// files. Leave empty to use the default /proc (production behaviour).
	// Tests inject a temp directory with fixture files here.
	procRoot string

	// infoClient is nil when dockerDataRoot was pinned by the caller. When set,
	// dockerDataRoot starts at defaultDockerDataRoot and is replaced by the
	// daemon's real DockerRootDir the first time /info answers.
	infoClient      DockerInfoClient
	dataRootKnown   bool
	dataRootRetryAt time.Time
}

// proc returns the effective proc root (defaults to /proc when empty).
func (c *Collector) proc() string {
	if c.procRoot != "" {
		return c.procRoot
	}
	return "/proc"
}

// NewCollector creates a new metrics collector against a fixed disk path.
// dockerDataRoot is the path to the Docker data directory (used for disk metrics).
// skipDisk disables disk metric collection when true.
func NewCollector(dockerDataRoot string, skipDisk bool) *Collector {
	return &Collector{
		dockerDataRoot: dockerDataRoot,
		skipDisk:       skipDisk,
		dataRootKnown:  true,
	}
}

// NewDaemonCollector creates a collector that asks the Docker daemon where its
// data root is instead of assuming /var/lib/docker, which is wrong on any host
// that moved it. The lookup is deferred to the first collection so agent
// startup never waits on the daemon, and a daemon that is unreachable then
// yields the default rather than an error.
func NewDaemonCollector(info DockerInfoClient, skipDisk bool) *Collector {
	return &Collector{
		dockerDataRoot: defaultDockerDataRoot,
		skipDisk:       skipDisk,
		infoClient:     info,
	}
}

// ErrHostMetricsUnsupported is returned by Collect when the host exposes no
// procfs, which is every platform except Linux. Callers should treat it as
// "this host cannot report these numbers" rather than as a transient failure.
var ErrHostMetricsUnsupported = errors.New("host metrics unavailable: no procfs on this platform")

// Collect gathers all host metrics and returns them.
//
// Every field except CPUCores is read from /proc. When /proc is absent the
// individual collect helpers each swallow their open error and leave a zero
// behind, which on the wire is indistinguishable from a real reading of a
// completely idle host: a native macOS install would report 0 bytes of memory
// and 0 bytes of disk as though it had measured them. Collect reports that gap
// as ErrHostMetricsUnsupported instead. The partially populated snapshot is
// still returned alongside the error, because CPUCores comes from
// runtime.NumCPU() and is correct on every platform.
//
// Disk is the exception to all of that: it comes from statfs rather than /proc,
// and a failed reading is reported in-band on the snapshot instead of as an
// error. See collectDisk.
func (c *Collector) Collect() (*HostMetrics, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	m := &HostMetrics{
		CPUCores: runtime.NumCPU(),
	}

	if _, err := os.Stat(c.proc()); err != nil {
		return m, fmt.Errorf("%w: %s: %w", ErrHostMetricsUnsupported, c.proc(), err)
	}

	m.CPUUsage = c.collectCPU()
	c.collectMemory(m)
	if !c.skipDisk {
		c.collectDisk(m)
	}
	c.collectNetwork(m)
	m.Uptime = c.collectUptime()

	return m, nil
}

// collectCPU reads /proc/stat and calculates delta-based CPU usage percentage.
// Returns 0 on the first call (no previous sample to compare against).
func (c *Collector) collectCPU() float64 {
	f, err := os.Open(filepath.Join(c.proc(), "stat"))
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}

		fields := strings.Fields(line)
		// Expected: cpu user nice system idle iowait irq softirq steal [guest guest_nice]
		if len(fields) < 9 {
			return 0
		}

		current := &cpuStats{}
		vals := make([]uint64, 8)
		for i := 0; i < 8; i++ {
			v, err := strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return 0
			}
			vals[i] = v
		}
		current.user = vals[0]
		current.nice = vals[1]
		current.system = vals[2]
		current.idle = vals[3]
		current.iowait = vals[4]
		current.irq = vals[5]
		current.softirq = vals[6]
		current.steal = vals[7]

		if c.prevCPU == nil {
			c.prevCPU = current
			return 0
		}

		deltaTotal := float64(current.total() - c.prevCPU.total())
		deltaIdle := float64(current.idleTotal() - c.prevCPU.idleTotal())

		c.prevCPU = current

		if deltaTotal == 0 {
			return 0
		}

		return (1 - (deltaIdle / deltaTotal)) * 100
	}

	return 0
}

// memInfo holds the /proc/meminfo fields the collector cares about, in bytes.
type memInfo struct {
	total        uint64
	free         uint64
	available    uint64
	buffers      uint64
	cached       uint64
	hasAvailable bool
}

// readMemInfo parses /proc/meminfo under the given proc root.
func readMemInfo(procRoot string) (memInfo, error) {
	var mi memInfo

	// #nosec G304 -- procRoot is the fixed "/proc" in production; tests inject a fixture temp dir.
	f, err := os.Open(filepath.Join(procRoot, "meminfo"))
	if err != nil {
		return mi, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.TrimSuffix(valStr, " kB")
		valStr = strings.TrimSpace(valStr)

		val, err := strconv.ParseUint(valStr, 10, 64)
		if err != nil {
			continue
		}
		// Convert kB to bytes.
		valBytes := val * 1024

		switch key {
		case "MemTotal":
			mi.total = valBytes
		case "MemFree":
			mi.free = valBytes
		case "MemAvailable":
			mi.available = valBytes
			mi.hasAvailable = true
		case "Buffers":
			mi.buffers = valBytes
		case "Cached":
			mi.cached = valBytes
		}
	}

	return mi, nil
}

// collectMemory reads /proc/meminfo and populates memory metrics.
func (c *Collector) collectMemory(m *HostMetrics) {
	mi, err := readMemInfo(c.proc())
	if err != nil {
		return
	}

	m.MemoryTotal = mi.total
	if mi.hasAvailable {
		m.MemoryFree = mi.available
	} else {
		m.MemoryFree = mi.free + mi.buffers + mi.cached
	}
	if m.MemoryTotal > m.MemoryFree {
		m.MemoryUsed = m.MemoryTotal - m.MemoryFree
	}
}

// MemoryTotalGB returns the total system memory in GiB rounded to one decimal
// place, or 0 when it cannot be determined (hosts without /proc/meminfo).
func MemoryTotalGB() float64 {
	return memoryTotalGB("/proc")
}

func memoryTotalGB(procRoot string) float64 {
	mi, err := readMemInfo(procRoot)
	if err != nil {
		return 0
	}
	gib := float64(mi.total) / (1 << 30)
	return math.Round(gib*10) / 10
}

// dataRoot returns the path collectDisk should measure, resolving it from the
// daemon on first use. A failed lookup is retried on a later collection rather
// than cached, because the common cause is a daemon that has not finished
// starting; until one succeeds the caller gets defaultDockerDataRoot, which is
// right for every host that did not move it. The caller holds c.mu.
func (c *Collector) dataRoot() string {
	if c.dataRootKnown || c.infoClient == nil {
		return c.dockerDataRoot
	}
	now := time.Now()
	if now.Before(c.dataRootRetryAt) {
		return c.dockerDataRoot
	}
	c.dataRootRetryAt = now.Add(dataRootRetryInterval)

	ctx, cancel := context.WithTimeout(context.Background(), dataRootTimeout)
	defer cancel()
	info, err := c.infoClient.GetDockerInfo(ctx)
	switch {
	case err != nil:
		slog.Debug("docker data root lookup failed, using default",
			"path", c.dockerDataRoot, "error", err)
	case info == nil || info.DockerRootDir == "":
		// The daemon answered and has nothing to report; retrying won't help.
		c.dataRootKnown = true
	default:
		c.dockerDataRoot = info.DockerRootDir
		c.dataRootKnown = true
		slog.Debug("resolved docker data root", "path", c.dockerDataRoot)
	}
	return c.dockerDataRoot
}

// collectDisk uses syscall.Statfs on the Docker data root to get disk usage.
//
// A failed Statfs leaves the byte counts at zero, which on the wire is
// indistinguishable from a real reading of an empty filesystem, so the failure
// is recorded on the snapshot instead of being swallowed. It is deliberately
// not returned from Collect: unlike a missing procfs it invalidates one metric
// group rather than all of them, and Collect's callers drop the whole snapshot
// on error.
func (c *Collector) collectDisk(m *HostMetrics) {
	root := c.dataRoot()
	var stat syscall.Statfs_t
	if err := syscall.Statfs(root, &stat); err != nil {
		m.DiskError = fmt.Sprintf("statfs %s: %v", root, err)
		return
	}
	blockSize := int64(stat.Bsize)
	if blockSize <= 0 {
		m.DiskError = fmt.Sprintf("statfs %s: unusable block size %d", root, blockSize)
		return
	}
	m.DiskTotal = statfsBytes(stat.Blocks, blockSize)
	m.DiskFree = statfsBytes(stat.Bavail, blockSize)
	m.DiskUsed = m.DiskTotal - m.DiskFree
	m.DiskMetricsAvailable = true
}

func statfsBytes(blocks uint64, blockSize int64) uint64 {
	if blockSize <= 0 {
		return 0
	}
	size := uint64(blockSize)
	if blocks > math.MaxUint64/size {
		return math.MaxUint64
	}
	return blocks * size
}

// collectNetwork reads /proc/net/dev and sums rx/tx bytes across all non-lo interfaces.
func (c *Collector) collectNetwork(m *HostMetrics) {
	f, err := os.Open(filepath.Join(c.proc(), "net", "dev"))
	if err != nil {
		return
	}
	defer f.Close()

	var totalRx, totalTx uint64

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		// Skip the two header lines.
		if lineNum <= 2 {
			continue
		}

		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue
		}

		fields := strings.Fields(parts[1])
		// Need at least 9 fields: rx_bytes(0) ... tx_bytes(8)
		if len(fields) < 9 {
			continue
		}

		rxBytes, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		txBytes, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			continue
		}

		totalRx += rxBytes
		totalTx += txBytes
	}

	m.NetworkRxBytes = totalRx
	m.NetworkTxBytes = totalTx
}

// collectUptime reads /proc/uptime and returns the system uptime in seconds.
func (c *Collector) collectUptime() uint64 {
	data, err := os.ReadFile(filepath.Join(c.proc(), "uptime"))
	if err != nil {
		return 0
	}

	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0
	}

	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}

	return uint64(uptime)
}
