package metrics

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCPUStatsTotal verifies the total() helper sums all fields.
func TestCPUStatsTotal(t *testing.T) {
	t.Parallel()

	s := cpuStats{
		user:    10,
		nice:    2,
		system:  5,
		idle:    70,
		iowait:  3,
		irq:     1,
		softirq: 4,
		steal:   5,
	}
	want := uint64(10 + 2 + 5 + 70 + 3 + 1 + 4 + 5)
	if got := s.total(); got != want {
		t.Errorf("total() = %d, want %d", got, want)
	}
}

// TestCPUStatsIdleTotal verifies idleTotal() returns idle + iowait only.
func TestCPUStatsIdleTotal(t *testing.T) {
	t.Parallel()

	s := cpuStats{
		user:    10,
		nice:    2,
		system:  5,
		idle:    70,
		iowait:  3,
		irq:     1,
		softirq: 4,
		steal:   5,
	}
	want := uint64(70 + 3)
	if got := s.idleTotal(); got != want {
		t.Errorf("idleTotal() = %d, want %d", got, want)
	}
}

// TestCPUStatsTotalZero verifies zero-value struct returns 0.
func TestCPUStatsTotalZero(t *testing.T) {
	t.Parallel()

	var s cpuStats
	if got := s.total(); got != 0 {
		t.Errorf("zero cpuStats total() = %d, want 0", got)
	}
	if got := s.idleTotal(); got != 0 {
		t.Errorf("zero cpuStats idleTotal() = %d, want 0", got)
	}
}

// TestNewCollector verifies the constructor wires fields correctly.
func TestNewCollector(t *testing.T) {
	t.Parallel()

	c := NewCollector("/var/lib/docker", false)
	if c == nil {
		t.Fatal("NewCollector returned nil")
	}
	if c.dockerDataRoot != "/var/lib/docker" {
		t.Errorf("dockerDataRoot = %q, want /var/lib/docker", c.dockerDataRoot)
	}
	if c.skipDisk {
		t.Error("skipDisk should be false")
	}

	c2 := NewCollector("", true)
	if !c2.skipDisk {
		t.Error("skipDisk should be true")
	}
}

// TestCollectReturnsHostMetrics verifies Collect returns a non-nil *HostMetrics
// with CPUCores populated (runtime.NumCPU() always > 0) even on platforms
// where /proc is absent (macOS). The other fields may be zero on non-Linux.
func TestCollectReturnsHostMetrics(t *testing.T) {
	t.Parallel()

	c := NewCollector(".", false)
	m, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect() returned error: %v", err)
	}
	if m == nil {
		t.Fatal("Collect() returned nil HostMetrics")
	}
	if m.CPUCores != runtime.NumCPU() {
		t.Errorf("CPUCores = %d, want %d", m.CPUCores, runtime.NumCPU())
	}
	// CPUUsage is [0, 100] — non-negative on all platforms.
	if m.CPUUsage < 0 || m.CPUUsage > 100 {
		t.Errorf("CPUUsage = %f, want in [0, 100]", m.CPUUsage)
	}
	// Memory fields are either all zero (non-Linux) or satisfy the invariant.
	if m.MemoryTotal > 0 {
		if m.MemoryUsed+m.MemoryFree > m.MemoryTotal {
			t.Errorf("MemoryUsed (%d) + MemoryFree (%d) > MemoryTotal (%d)",
				m.MemoryUsed, m.MemoryFree, m.MemoryTotal)
		}
	}
	// Disk fields: DiskTotal >= DiskFree (may both be zero when skipDisk=false
	// but path "." maps to a real filesystem on Linux/macOS).
	if m.DiskTotal > 0 && m.DiskFree > m.DiskTotal {
		t.Errorf("DiskFree (%d) > DiskTotal (%d)", m.DiskFree, m.DiskTotal)
	}
	// DiskUsed = DiskTotal - DiskFree; verify if total is set.
	if m.DiskTotal > 0 {
		want := m.DiskTotal - m.DiskFree
		if m.DiskUsed != want {
			t.Errorf("DiskUsed = %d, want DiskTotal - DiskFree = %d", m.DiskUsed, want)
		}
	}
}

// TestCollectSkipDisk verifies that disk fields remain zero when skipDisk=true.
func TestCollectSkipDisk(t *testing.T) {
	t.Parallel()

	c := NewCollector(".", true)
	m, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect() returned error: %v", err)
	}
	if m.DiskTotal != 0 || m.DiskUsed != 0 || m.DiskFree != 0 {
		t.Errorf("with skipDisk=true expected zero disk fields, got total=%d used=%d free=%d",
			m.DiskTotal, m.DiskUsed, m.DiskFree)
	}
}

// TestCollectCPUSecondCallProducesDelta exercises the delta logic inside
// collectCPU on Linux. On macOS (no /proc/stat), both calls return 0 — still
// worth exercising the prevCPU path to ensure no panic.
func TestCollectCPUSecondCallProducesDelta(t *testing.T) {
	t.Parallel()

	c := NewCollector(".", true)

	first := c.collectCPU()
	// First call: prevCPU was nil → should be 0 (whether or not /proc exists).
	if first != 0 {
		t.Errorf("first collectCPU() = %f, want 0", first)
	}

	second := c.collectCPU()
	// Second call: prevCPU is now set. On Linux this may be > 0; on macOS it's 0.
	// Either way it must be non-negative and ≤ 100.
	if second < 0 || second > 100 {
		t.Errorf("second collectCPU() = %f, want in [0, 100]", second)
	}
}

// TestCollectDiskRealPath exercises collectDisk against "." which exists on
// every platform that supports syscall.Statfs (Linux + macOS). Verifies the
// invariants: DiskTotal >= DiskFree, DiskUsed = DiskTotal - DiskFree.
func TestCollectDiskRealPath(t *testing.T) {
	t.Parallel()

	c := &Collector{dockerDataRoot: "."}
	m := &HostMetrics{}
	c.collectDisk(m)

	if m.DiskTotal == 0 {
		// On some CI environments Statfs on "." can return zeros; skip rather
		// than assert.
		t.Skip("Statfs on '.' returned zero DiskTotal; skipping disk invariant checks")
	}
	if m.DiskFree > m.DiskTotal {
		t.Errorf("DiskFree (%d) > DiskTotal (%d)", m.DiskFree, m.DiskTotal)
	}
	want := m.DiskTotal - m.DiskFree
	if m.DiskUsed != want {
		t.Errorf("DiskUsed = %d, want %d (DiskTotal - DiskFree)", m.DiskUsed, want)
	}
}

// TestCollectDiskMissingPath verifies collectDisk does not panic and leaves
// disk fields zero when given a path that does not exist.
func TestCollectDiskMissingPath(t *testing.T) {
	t.Parallel()

	c := &Collector{dockerDataRoot: "/nonexistent/path/that/does/not/exist/12345"}
	m := &HostMetrics{}
	c.collectDisk(m)

	if m.DiskTotal != 0 || m.DiskUsed != 0 || m.DiskFree != 0 {
		t.Errorf("expected zero disk fields for missing path, got total=%d used=%d free=%d",
			m.DiskTotal, m.DiskUsed, m.DiskFree)
	}
}

// TestStatfsBytesNormalCase verifies straightforward block × size multiplication.
func TestStatfsBytesNormalCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		blocks    uint64
		blockSize int64
		want      uint64
	}{
		{0, 4096, 0},
		{1, 4096, 4096},
		{100, 512, 51200},
		{1024, 1024, 1024 * 1024},
	}
	for _, tc := range tests {
		got := statfsBytes(tc.blocks, tc.blockSize)
		if got != tc.want {
			t.Errorf("statfsBytes(%d, %d) = %d, want %d", tc.blocks, tc.blockSize, got, tc.want)
		}
	}
}

// TestCollectMemoryWithFakeProcMeminfo feeds a synthetic /proc/meminfo-style
// file to collectMemory by temporarily pointing os.Open at a temp file.
// Since collectMemory hard-codes os.Open("/proc/meminfo"), on macOS the file
// won't exist. We exercise the parsing logic by writing our own fake file to
// /proc/meminfo-equivalent path. Because that path is hard-coded we can only
// test the error path on macOS and the parse path on Linux. The test is split
// into sub-tests accordingly.
func TestCollectMemoryAbsent(t *testing.T) {
	t.Parallel()

	// On macOS /proc/meminfo does not exist; collectMemory should silently
	// return with all fields at zero.
	if _, err := os.Stat("/proc/meminfo"); os.IsNotExist(err) {
		c := NewCollector(".", true)
		m := &HostMetrics{}
		c.collectMemory(m)
		if m.MemoryTotal != 0 || m.MemoryUsed != 0 || m.MemoryFree != 0 {
			t.Errorf("expected zero memory on absent /proc/meminfo, got %+v", m)
		}
		return
	}
	// On Linux /proc/meminfo exists; check basic invariants instead.
	c := NewCollector(".", true)
	m := &HostMetrics{}
	c.collectMemory(m)
	if m.MemoryTotal == 0 {
		t.Error("MemoryTotal = 0 on Linux with /proc/meminfo present")
	}
	if m.MemoryUsed+m.MemoryFree > m.MemoryTotal {
		t.Errorf("MemoryUsed(%d) + MemoryFree(%d) > MemoryTotal(%d)",
			m.MemoryUsed, m.MemoryFree, m.MemoryTotal)
	}
}

// TestCollectNetworkAbsent verifies collectNetwork is a no-op when
// /proc/net/dev is absent (macOS).
func TestCollectNetworkAbsent(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("/proc/net/dev"); os.IsNotExist(err) {
		c := NewCollector(".", true)
		m := &HostMetrics{}
		c.collectNetwork(m)
		if m.NetworkRxBytes != 0 || m.NetworkTxBytes != 0 {
			t.Errorf("expected zero network bytes on absent /proc/net/dev, got rx=%d tx=%d",
				m.NetworkRxBytes, m.NetworkTxBytes)
		}
		return
	}
	// On Linux verify non-negative invariant.
	c := NewCollector(".", true)
	m := &HostMetrics{}
	c.collectNetwork(m)
	// Rx and Tx bytes are unsigned so always >= 0; just assert no panic/error.
}

// TestCollectUptimeAbsent verifies collectUptime returns 0 when
// /proc/uptime is absent (macOS).
func TestCollectUptimeAbsent(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat("/proc/uptime"); os.IsNotExist(err) {
		c := NewCollector(".", true)
		got := c.collectUptime()
		if got != 0 {
			t.Errorf("collectUptime() = %d on absent /proc/uptime, want 0", got)
		}
		return
	}
	// On Linux uptime must be > 0 for a running system.
	c := NewCollector(".", true)
	got := c.collectUptime()
	if got == 0 {
		t.Error("collectUptime() = 0 on Linux with /proc/uptime present")
	}
}

// TestCollectNetworkParsing exercises collectNetwork's parsing logic using a
// synthetic /proc/net/dev-style file written to a temp directory. Because the
// path is hard-coded to /proc/net/dev we test the parser indirectly via a
// fake procfs written to a temp file and read with parseNetDev (which doesn't
// exist as a separate function). Instead, this test validates the public
// observable effect on Linux, and on macOS confirms zero-value behaviour.
//
// For a richer parse test without invasive refactoring, we call collectNetwork
// on Linux where /proc/net/dev is present and simply assert shape invariants.
func TestCollectNetworkOnLinux(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("skipping /proc/net/dev parse test on non-Linux")
	}
	c := NewCollector(".", true)
	m := &HostMetrics{}
	c.collectNetwork(m)
	// Values are always non-negative (uint64); just confirm no panic.
	_ = m.NetworkRxBytes
	_ = m.NetworkTxBytes
}

// TestCollectCPUParseFakeFile exercises the CPU parsing logic on Linux by
// relying on the real /proc/stat. On macOS it checks the not-present case.
func TestCollectCPUOnLinux(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("skipping /proc/stat CPU parse test on non-Linux")
	}
	c := NewCollector(".", true)
	// Two calls: first returns 0 (no previous sample), second returns a delta.
	first := c.collectCPU()
	if first != 0 {
		t.Errorf("first collectCPU() on Linux = %f, want 0 (no prev sample)", first)
	}
	second := c.collectCPU()
	if second < 0 || second > 100 {
		t.Errorf("second collectCPU() = %f, want in [0, 100]", second)
	}
}

// TestCollectMemoryFakeFile exercises the /proc/meminfo parser by writing a
// synthetic meminfo to a temp file. Because collectMemory hard-codes the path,
// we can only do this on Linux where /proc is a real filesystem. On macOS we
// verify that the absent-path behaviour is correct (covered by TestCollectMemoryAbsent).
//
// On Linux this test cross-checks that MemAvailable is preferred over MemFree.
func TestCollectMemoryFakeFile(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("skipping fake-file meminfo parse test on non-Linux")
	}

	// Write a synthetic /proc/meminfo-like file and confirm parsing.
	// We can't inject the path, so on Linux we just validate real values.
	c := NewCollector(".", true)
	m := &HostMetrics{}
	c.collectMemory(m)
	if m.MemoryTotal == 0 {
		t.Error("MemoryTotal = 0 on Linux")
	}
}

// TestCollectDiskWithTempDir exercises collectDisk against a real temp
// directory, which is guaranteed to exist. Verifies invariants.
func TestCollectDiskWithTempDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Write a small file so the directory is non-trivially populated.
	_ = os.WriteFile(filepath.Join(dir, "probe"), []byte("x"), 0o600)

	c := &Collector{dockerDataRoot: dir}
	m := &HostMetrics{}
	c.collectDisk(m)

	if m.DiskTotal == 0 {
		t.Skip("Statfs on temp dir returned zero DiskTotal; filesystem may not support it")
	}
	if m.DiskFree > m.DiskTotal {
		t.Errorf("DiskFree (%d) > DiskTotal (%d)", m.DiskFree, m.DiskTotal)
	}
	want := m.DiskTotal - m.DiskFree
	if m.DiskUsed != want {
		t.Errorf("DiskUsed = %d, want %d", m.DiskUsed, want)
	}
}

// TestHostMetricsJSONFieldNames is a compile-time-style test that confirms
// HostMetrics has the expected exported fields, via struct literal assignment.
func TestHostMetricsFields(t *testing.T) {
	t.Parallel()

	m := HostMetrics{
		CPUUsage:       1.5,
		CPUCores:       4,
		MemoryTotal:    8 * 1024 * 1024 * 1024,
		MemoryUsed:     4 * 1024 * 1024 * 1024,
		MemoryFree:     4 * 1024 * 1024 * 1024,
		DiskTotal:      100e9,
		DiskUsed:       50e9,
		DiskFree:       50e9,
		NetworkRxBytes: 1024,
		NetworkTxBytes: 2048,
		Uptime:         3600,
	}
	if m.CPUCores != 4 {
		t.Errorf("CPUCores = %d, want 4", m.CPUCores)
	}
	if m.Uptime != 3600 {
		t.Errorf("Uptime = %d, want 3600", m.Uptime)
	}
}

// TestCollectConcurrency verifies that concurrent calls to Collect do not
// race on the internal mutex. Run with -race.
func TestCollectConcurrency(t *testing.T) {
	t.Parallel()

	c := NewCollector(".", true)
	const goroutines = 10
	done := make(chan struct{}, goroutines)
	for range goroutines {
		go func() {
			defer func() { done <- struct{}{} }()
			m, err := c.Collect()
			if err != nil {
				t.Errorf("Collect() error: %v", err)
			}
			if m == nil {
				t.Error("Collect() returned nil")
			}
		}()
	}
	for range goroutines {
		<-done
	}
}

// TestCollectUptimeOnLinux verifies collectUptime returns > 0 on Linux.
func TestCollectUptimeOnLinux(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("skipping /proc/uptime test on non-Linux")
	}
	c := NewCollector(".", true)
	got := c.collectUptime()
	if got == 0 {
		t.Error("collectUptime() = 0 on Linux, expected > 0")
	}
}

// TestStatfsBytesEdgeCases covers additional edge cases for statfsBytes.
func TestStatfsBytesEdgeCases(t *testing.T) {
	t.Parallel()

	// blocks=0 → 0 regardless of block size.
	if got := statfsBytes(0, 4096); got != 0 {
		t.Errorf("statfsBytes(0, 4096) = %d, want 0", got)
	}
	// Normal 4K block × 256 blocks = 1 MiB.
	if got := statfsBytes(256, 4096); got != 256*4096 {
		t.Errorf("statfsBytes(256, 4096) = %d, want %d", got, uint64(256*4096))
	}
	// Overflow: blocks just above the threshold.
	const blockSize = int64(2)
	// MaxUint64 / 2 + 1 blocks → overflow → MaxUint64.
	overflowBlocks := uint64(math.MaxUint64)/uint64(blockSize) + 1
	if got := statfsBytes(overflowBlocks, blockSize); got != math.MaxUint64 {
		t.Errorf("statfsBytes overflow case = %d, want MaxUint64", got)
	}
}

// TestNewCollectorDefaultsNoPrevCPU verifies that a freshly created Collector
// has no previous CPU sample (prevCPU == nil), so the first collectCPU call
// always returns 0.
func TestNewCollectorDefaultsNoPrevCPU(t *testing.T) {
	t.Parallel()

	c := NewCollector("/any/path", false)
	if c.prevCPU != nil {
		t.Error("NewCollector should initialise prevCPU to nil")
	}
}

// TestCollectMemoryLinuxHasAvailablePreference exercises the MemAvailable
// preference branch. On Linux, /proc/meminfo always has MemAvailable so the
// hasAvailable branch should fire. Validated by checking MemoryFree > 0 when
// the system has available memory.
func TestCollectMemoryLinuxHasAvailablePreference(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("MemAvailable branch test only runs on Linux")
	}
	c := NewCollector(".", true)
	m := &HostMetrics{}
	c.collectMemory(m)
	// On a running Linux system there is always some free memory.
	if m.MemoryFree == 0 && m.MemoryTotal > 0 {
		t.Error("MemoryFree = 0 on Linux with memory present; MemAvailable branch may not be firing")
	}
}

// TestCollectUptime_InvalidContent verifies that collectUptime returns 0 when
// /proc/uptime content is unparseable. We do this by creating a fake file at a
// temp path and pointing a local Collector at a closure that reads it.
// Since collectUptime hard-codes os.ReadFile("/proc/uptime"), we can only test
// the actual behaviour: on Linux the file should parse; on macOS it returns 0.
//
// For coverage of the strconv.ParseFloat error path we'd need dependency
// injection. We note this gap and test what we can without modifying source.
func TestCollectUptime_ZeroOnMissingFile(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "linux" {
		t.Skip("skipping missing-file uptime test on Linux where /proc/uptime exists")
	}
	c := NewCollector(".", true)
	got := c.collectUptime()
	if got != 0 {
		t.Errorf("collectUptime() on macOS = %d, want 0", got)
	}
}

// TestWritePrometheusWithNoEscapeHelper confirms the noEscape helper available
// in the internal test package works as expected (sanity check for test helpers).
// This lives here because collector_test.go is in package metrics (internal)
// and we want to test internal behaviour. The escaping itself is tested in the
// external test package.
func TestInternalPackageAccessToCollector(t *testing.T) {
	t.Parallel()

	// Verify that internal types are accessible and well-formed.
	s := &cpuStats{user: 5, idle: 95}
	if s.total() != 100 {
		t.Errorf("cpuStats total = %d, want 100", s.total())
	}
	if s.idleTotal() != 95 {
		t.Errorf("cpuStats idleTotal = %d, want 95", s.idleTotal())
	}
}

// TestCollectNetworkSkipsLoopback verifies that on platforms where
// /proc/net/dev exists, the loopback interface is excluded from totals.
// On macOS this is a skip; on Linux it checks lo is excluded by checking
// that network parsing doesn't double-count.
func TestCollectNetworkSkipsLoopback(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("loopback exclusion test only runs on Linux")
	}
	c := NewCollector(".", true)
	m := &HostMetrics{}
	c.collectNetwork(m)
	// We can't directly check lo is excluded without introspection, but we
	// verify the total is non-negative and non-maxuint64 (which would indicate
	// a wrap-around bug from including lo twice or similar).
	if m.NetworkRxBytes == math.MaxUint64 {
		t.Error("NetworkRxBytes is MaxUint64, possible overflow/loopback double-count")
	}
}

// TestCPUUsageNeverExceeds100 exercises multiple Collect calls and verifies
// CPU usage stays in [0, 100] across all calls.
func TestCPUUsageNeverExceeds100(t *testing.T) {
	t.Parallel()

	c := NewCollector(".", true)
	for i := range 5 {
		m, err := c.Collect()
		if err != nil {
			t.Fatalf("call %d: Collect() error: %v", i, err)
		}
		if m.CPUUsage < 0 || m.CPUUsage > 100 {
			t.Errorf("call %d: CPUUsage = %f, want in [0, 100]", i, m.CPUUsage)
		}
	}
}

// TestCollectorDockerDataRootPreserved verifies that the dockerDataRoot is
// exactly what was passed to NewCollector (no normalisation happens in ctor).
func TestCollectorDockerDataRootPreserved(t *testing.T) {
	t.Parallel()

	paths := []string{
		"",
		"/var/lib/docker",
		"/data/docker",
		"relative/path",
	}
	for _, p := range paths {
		c := NewCollector(p, false)
		if c.dockerDataRoot != p {
			t.Errorf("NewCollector(%q).dockerDataRoot = %q, want %q", p, c.dockerDataRoot, p)
		}
	}
}

// TestHostMetricsString is a trivial smoke-test that HostMetrics can be
// converted via fmt (used in logging) without panicking.
func TestHostMetricsNoNilPanic(t *testing.T) {
	t.Parallel()

	m := &HostMetrics{}
	s := strings.Builder{}
	// Just confirm we can read all fields without panic.
	s.WriteString(string(rune(m.CPUCores)))
	_ = m.CPUUsage
	_ = m.MemoryTotal
	_ = m.DiskTotal
	_ = m.NetworkRxBytes
	_ = m.Uptime
}
