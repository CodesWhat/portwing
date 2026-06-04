package metrics

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
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
	prevTime       time.Time
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
	f, err := os.Open("/proc/stat")
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

		now := time.Now()

		if c.prevCPU == nil {
			c.prevCPU = current
			c.prevTime = now
			return 0
		}

		deltaTotal := float64(current.total() - c.prevCPU.total())
		deltaIdle := float64(current.idleTotal() - c.prevCPU.idleTotal())

		c.prevCPU = current
		c.prevTime = now

		if deltaTotal == 0 {
			return 0
		}

		return (1 - (deltaIdle / deltaTotal)) * 100
	}

	return 0
}

// collectMemory reads /proc/meminfo and populates memory metrics.
func (c *Collector) collectMemory(m *HostMetrics) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()

	var (
		memTotal     uint64
		memFree      uint64
		memAvailable uint64
		buffers      uint64
		cached       uint64
		hasAvailable bool
	)

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
			memTotal = valBytes
		case "MemFree":
			memFree = valBytes
		case "MemAvailable":
			memAvailable = valBytes
			hasAvailable = true
		case "Buffers":
			buffers = valBytes
		case "Cached":
			cached = valBytes
		}
	}

	m.MemoryTotal = memTotal
	if hasAvailable {
		m.MemoryFree = memAvailable
	} else {
		m.MemoryFree = memFree + buffers + cached
	}
	if m.MemoryTotal > m.MemoryFree {
		m.MemoryUsed = m.MemoryTotal - m.MemoryFree
	}
}

// collectDisk uses syscall.Statfs on the Docker data root to get disk usage.
func (c *Collector) collectDisk(m *HostMetrics) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(c.dockerDataRoot, &stat); err != nil {
		return
	}
	m.DiskTotal = stat.Blocks * uint64(stat.Bsize)
	m.DiskFree = stat.Bavail * uint64(stat.Bsize)
	m.DiskUsed = m.DiskTotal - m.DiskFree
}

// collectNetwork reads /proc/net/dev and sums rx/tx bytes across all non-lo interfaces.
func (c *Collector) collectNetwork(m *HostMetrics) {
	f, err := os.Open("/proc/net/dev")
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
	data, err := os.ReadFile("/proc/uptime")
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

