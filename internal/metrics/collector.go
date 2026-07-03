package metrics

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// HostMetrics contains system-level resource metrics.
type HostMetrics struct {
	CPUUsage       float64 `json:"cpuUsage"`
	CPUCores       int     `json:"cpuCores"`
	MemoryTotal    uint64  `json:"memoryTotal"`
	MemoryUsed     uint64  `json:"memoryUsed"`
	MemoryFree     uint64  `json:"memoryFree"`
	DiskTotal      uint64  `json:"diskTotal"`
	DiskUsed       uint64  `json:"diskUsed"`
	DiskFree       uint64  `json:"diskFree"`
	NetworkRxBytes uint64  `json:"networkRxBytes"`
	NetworkTxBytes uint64  `json:"networkTxBytes"`
	Uptime         uint64  `json:"uptime"`
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
}

// proc returns the effective proc root (defaults to /proc when empty).
func (c *Collector) proc() string {
	if c.procRoot != "" {
		return c.procRoot
	}
	return "/proc"
}

// NewCollector creates a new metrics collector.
// dockerDataRoot is the path to the Docker data directory (used for disk metrics).
// skipDisk disables disk metric collection when true.
func NewCollector(dockerDataRoot string, skipDisk bool) *Collector {
	return &Collector{
		dockerDataRoot: dockerDataRoot,
		skipDisk:       skipDisk,
	}
}

// Collect gathers all host metrics and returns them.
func (c *Collector) Collect() (*HostMetrics, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	m := &HostMetrics{
		CPUCores: runtime.NumCPU(),
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

// collectDisk uses syscall.Statfs on the Docker data root to get disk usage.
func (c *Collector) collectDisk(m *HostMetrics) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(c.dockerDataRoot, &stat); err != nil {
		return
	}
	blockSize := int64(stat.Bsize)
	m.DiskTotal = statfsBytes(stat.Blocks, blockSize)
	m.DiskFree = statfsBytes(stat.Bavail, blockSize)
	m.DiskUsed = m.DiskTotal - m.DiskFree
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
