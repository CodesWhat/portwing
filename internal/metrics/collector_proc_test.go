// Package metrics — fixture-driven tests for /proc file parsers.
// These tests inject a temp directory as the procRoot so every parse branch
// runs on darwin and linux alike without touching the real /proc.
package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

// mkProcFixture writes content to path inside a base dir, creating all
// intermediate directories. Returns the base dir.
func mkProcFixture(t *testing.T, base, relPath, content string) {
	t.Helper()
	full := filepath.Join(base, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", full, err)
	}
}

// collectorWithProc returns a Collector whose procRoot points at dir.
func collectorWithProc(dir string) *Collector {
	return &Collector{procRoot: dir, skipDisk: true}
}

// ---------------------------------------------------------------------------
// collectCPU tests
// ---------------------------------------------------------------------------

// TestCollectCPUFixtureFirstCall verifies the first call with a valid /proc/stat
// returns 0 (no previous sample) and stores prevCPU.
func TestCollectCPUFixtureFirstCall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkProcFixture(t, dir, "stat",
		"cpu  1000 200 300 8000 100 50 25 10\ncpu0 500 100 150 4000 50 25 12 5\n")

	c := collectorWithProc(dir)
	got := c.collectCPU()
	if got != 0 {
		t.Errorf("first collectCPU() = %f, want 0", got)
	}
	if c.prevCPU == nil {
		t.Error("prevCPU should be set after first call")
	}
}

// TestCollectCPUFixtureDelta verifies the second call computes a correct delta.
func TestCollectCPUFixtureDelta(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// First sample: total=9685, idle=8100
	const stat1 = "cpu  1000 200 300 8000 100 50 25 10\n"
	mkProcFixture(t, dir, "stat", stat1)
	c := collectorWithProc(dir)
	c.collectCPU() // primes prevCPU

	// Second sample: user increases by 500, everything else same.
	// new total=10185, new idle=8100  →  deltaTotal=500, deltaIdle=0
	// usage = (1 - 0/500)*100 = 100.0
	const stat2 = "cpu  1500 200 300 8000 100 50 25 10\n"
	mkProcFixture(t, dir, "stat", stat2)
	got := c.collectCPU()
	if got != 100.0 {
		t.Errorf("collectCPU() delta = %f, want 100.0", got)
	}
}

// TestCollectCPUFixtureDeltaZero verifies zero-delta branch returns 0.
func TestCollectCPUFixtureDeltaZero(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const statContent = "cpu  1000 200 300 8000 100 50 25 10\n"
	mkProcFixture(t, dir, "stat", statContent)
	c := collectorWithProc(dir)
	c.collectCPU() // first call — prime prevCPU

	// Same data again → deltaTotal == 0 → returns 0.
	got := c.collectCPU()
	if got != 0 {
		t.Errorf("collectCPU() zero-delta = %f, want 0", got)
	}
}

// TestCollectCPUFixtureShortFields verifies < 9 fields returns 0.
func TestCollectCPUFixtureShortFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Only 5 fields after "cpu" — too few.
	mkProcFixture(t, dir, "stat", "cpu  100 200 300 400\n")
	c := collectorWithProc(dir)
	got := c.collectCPU()
	if got != 0 {
		t.Errorf("collectCPU() short fields = %f, want 0", got)
	}
}

// TestCollectCPUFixtureBadField verifies a non-numeric field returns 0.
func TestCollectCPUFixtureBadField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkProcFixture(t, dir, "stat", "cpu  1000 200 NaN 8000 100 50 25 10\n")
	c := collectorWithProc(dir)
	got := c.collectCPU()
	if got != 0 {
		t.Errorf("collectCPU() bad field = %f, want 0", got)
	}
}

// TestCollectCPUFixtureNoCPULine verifies a stat file with no "cpu " line returns 0.
func TestCollectCPUFixtureNoCPULine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkProcFixture(t, dir, "stat", "intr 12345\nbtime 1700000000\n")
	c := collectorWithProc(dir)
	got := c.collectCPU()
	if got != 0 {
		t.Errorf("collectCPU() no cpu line = %f, want 0", got)
	}
}

// TestCollectCPUFixtureMixedCPULines verifies only the "cpu " aggregate line is used.
func TestCollectCPUFixtureMixedCPULines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const content = "cpu  1000 200 300 8000 100 50 25 10\ncpu0 500 100 150 4000 50 25 12 5\ncpu1 500 100 150 4000 50 25 13 5\n"
	mkProcFixture(t, dir, "stat", content)
	c := collectorWithProc(dir)
	c.collectCPU() // first call

	// Second call with slightly higher user on aggregate line.
	const content2 = "cpu  1100 200 300 8000 100 50 25 10\ncpu0 550 100 150 4000 50 25 12 5\ncpu1 550 100 150 4000 50 25 13 5\n"
	mkProcFixture(t, dir, "stat", content2)
	got := c.collectCPU()
	// deltaTotal=100, deltaIdle=0 → 100%
	if got != 100.0 {
		t.Errorf("collectCPU() mixed lines = %f, want 100.0", got)
	}
}

// TestCollectCPUFixturePartialUsage verifies fractional CPU usage math.
func TestCollectCPUFixturePartialUsage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Sample 1: all-idle baseline.
	mkProcFixture(t, dir, "stat", "cpu  0 0 0 1000 0 0 0 0\n")
	c := collectorWithProc(dir)
	c.collectCPU()

	// Sample 2: 500 idle, 500 user → 50% usage.
	mkProcFixture(t, dir, "stat", "cpu  500 0 0 1500 0 0 0 0\n")
	got := c.collectCPU()
	// deltaTotal=1000, deltaIdle=500 → (1-0.5)*100 = 50.0
	if got != 50.0 {
		t.Errorf("collectCPU() partial usage = %f, want 50.0", got)
	}
}

// ---------------------------------------------------------------------------
// collectMemory tests
// ---------------------------------------------------------------------------

// TestCollectMemoryFixtureWithAvailable verifies MemAvailable is preferred over
// MemFree+Buffers+Cached when MemAvailable is present.
func TestCollectMemoryFixtureWithAvailable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkProcFixture(t, dir, "meminfo", `MemTotal:       8192000 kB
MemFree:        1024000 kB
MemAvailable:   2048000 kB
Buffers:         512000 kB
Cached:          256000 kB
SwapTotal:      4096000 kB
SwapFree:       4096000 kB
`)
	c := collectorWithProc(dir)
	m := &HostMetrics{}
	c.collectMemory(m)

	wantTotal := uint64(8192000 * 1024)
	wantFree := uint64(2048000 * 1024) // MemAvailable wins
	wantUsed := wantTotal - wantFree

	if m.MemoryTotal != wantTotal {
		t.Errorf("MemoryTotal = %d, want %d", m.MemoryTotal, wantTotal)
	}
	if m.MemoryFree != wantFree {
		t.Errorf("MemoryFree = %d, want %d (MemAvailable)", m.MemoryFree, wantFree)
	}
	if m.MemoryUsed != wantUsed {
		t.Errorf("MemoryUsed = %d, want %d", m.MemoryUsed, wantUsed)
	}
}

// TestCollectMemoryFixtureWithoutAvailable verifies MemFree+Buffers+Cached is
// used when MemAvailable is absent.
func TestCollectMemoryFixtureWithoutAvailable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkProcFixture(t, dir, "meminfo", `MemTotal:       4096000 kB
MemFree:         512000 kB
Buffers:         256000 kB
Cached:          512000 kB
`)
	c := collectorWithProc(dir)
	m := &HostMetrics{}
	c.collectMemory(m)

	wantTotal := uint64(4096000 * 1024)
	wantFree := uint64((512000 + 256000 + 512000) * 1024) // MemFree+Buffers+Cached
	wantUsed := wantTotal - wantFree

	if m.MemoryTotal != wantTotal {
		t.Errorf("MemoryTotal = %d, want %d", m.MemoryTotal, wantTotal)
	}
	if m.MemoryFree != wantFree {
		t.Errorf("MemoryFree = %d, want %d (fallback)", m.MemoryFree, wantFree)
	}
	if m.MemoryUsed != wantUsed {
		t.Errorf("MemoryUsed = %d, want %d", m.MemoryUsed, wantUsed)
	}
}

// TestCollectMemoryFixtureFreeExceedsTotal verifies MemoryUsed stays 0 when
// MemoryFree >= MemoryTotal (the branch `if m.MemoryTotal > m.MemoryFree`).
func TestCollectMemoryFixtureFreeExceedsTotal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// MemAvailable > MemTotal — pathological but possible with overcommit.
	mkProcFixture(t, dir, "meminfo", `MemTotal:       1000 kB
MemFree:          500 kB
MemAvailable:    1500 kB
Buffers:            0 kB
Cached:             0 kB
`)
	c := collectorWithProc(dir)
	m := &HostMetrics{}
	c.collectMemory(m)

	if m.MemoryUsed != 0 {
		t.Errorf("MemoryUsed = %d, want 0 when MemoryFree >= MemoryTotal", m.MemoryUsed)
	}
}

// TestCollectMemoryFixtureMalformedLine verifies lines without ":" are skipped.
func TestCollectMemoryFixtureMalformedLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkProcFixture(t, dir, "meminfo", `MemTotal:       2048000 kB
this line has no colon
MemFree:         1024000 kB
MemAvailable:    1024000 kB
Buffers:             0 kB
Cached:              0 kB
`)
	c := collectorWithProc(dir)
	m := &HostMetrics{}
	c.collectMemory(m)
	if m.MemoryTotal == 0 {
		t.Error("MemoryTotal = 0, malformed line should have been skipped not killed parsing")
	}
}

// TestCollectMemoryFixtureBadValue verifies a non-numeric value is skipped.
func TestCollectMemoryFixtureBadValue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkProcFixture(t, dir, "meminfo", `MemTotal:       NOTANUMBER kB
MemFree:         1024000 kB
MemAvailable:    1024000 kB
`)
	c := collectorWithProc(dir)
	m := &HostMetrics{}
	c.collectMemory(m)
	// MemTotal was unparseable, so remains 0; MemFree = MemAvailable = 1024000*1024.
	if m.MemoryTotal != 0 {
		t.Errorf("MemoryTotal = %d, want 0 (bad value skipped)", m.MemoryTotal)
	}
}

// ---------------------------------------------------------------------------
// collectNetwork tests
// ---------------------------------------------------------------------------

const netDevHeader = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
`

// TestCollectNetworkFixtureBasic verifies rx and tx bytes are summed across
// non-loopback interfaces.
func TestCollectNetworkFixtureBasic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := netDevHeader +
		"    lo:  100000     500    0    0    0     0          0         0   100000     500    0    0    0     0       0          0\n" +
		"  eth0: 2000000    1000    0    0    0     0          0         0  1000000     500    0    0    0     0       0          0\n" +
		"  eth1:  500000     250    0    0    0     0          0         0   250000     125    0    0    0     0       0          0\n"
	mkProcFixture(t, dir, filepath.Join("net", "dev"), content)

	c := collectorWithProc(dir)
	m := &HostMetrics{}
	c.collectNetwork(m)

	// lo excluded; eth0+eth1 summed.
	wantRx := uint64(2000000 + 500000)
	wantTx := uint64(1000000 + 250000)
	if m.NetworkRxBytes != wantRx {
		t.Errorf("NetworkRxBytes = %d, want %d", m.NetworkRxBytes, wantRx)
	}
	if m.NetworkTxBytes != wantTx {
		t.Errorf("NetworkTxBytes = %d, want %d", m.NetworkTxBytes, wantTx)
	}
}

// TestCollectNetworkFixtureLoopbackOnly verifies all-loopback gives zero totals.
func TestCollectNetworkFixtureLoopbackOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := netDevHeader +
		"    lo: 9999999    9999    0    0    0     0          0         0  9999999    9999    0    0    0     0       0          0\n"
	mkProcFixture(t, dir, filepath.Join("net", "dev"), content)

	c := collectorWithProc(dir)
	m := &HostMetrics{}
	c.collectNetwork(m)

	if m.NetworkRxBytes != 0 || m.NetworkTxBytes != 0 {
		t.Errorf("loopback-only: rx=%d tx=%d, want 0 0", m.NetworkRxBytes, m.NetworkTxBytes)
	}
}

// TestCollectNetworkFixtureShortFields verifies data lines with fewer than 9
// fields after the colon are skipped without panic.
func TestCollectNetworkFixtureShortFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := netDevHeader +
		// Only 3 fields — too few.
		"  eth0:  12345     100    0\n" +
		// Valid line that DOES contribute.
		"  eth1: 2000000    1000    0    0    0     0          0         0  1000000     500    0    0    0     0       0          0\n"
	mkProcFixture(t, dir, filepath.Join("net", "dev"), content)

	c := collectorWithProc(dir)
	m := &HostMetrics{}
	c.collectNetwork(m)

	// eth0 skipped (short), eth1 counted.
	if m.NetworkRxBytes != 2000000 {
		t.Errorf("NetworkRxBytes = %d, want 2000000 (short-fields line skipped)", m.NetworkRxBytes)
	}
}

// TestCollectNetworkFixtureBadRxBytes verifies a non-numeric rx field is skipped.
func TestCollectNetworkFixtureBadRxBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := netDevHeader +
		"  eth0:  BADVAL    1000    0    0    0     0          0         0  1000000     500    0    0    0     0       0          0\n" +
		"  eth1: 2000000    1000    0    0    0     0          0         0  1000000     500    0    0    0     0       0          0\n"
	mkProcFixture(t, dir, filepath.Join("net", "dev"), content)

	c := collectorWithProc(dir)
	m := &HostMetrics{}
	c.collectNetwork(m)

	// eth0 rx parse fails → skip; eth1 counts.
	if m.NetworkRxBytes != 2000000 {
		t.Errorf("NetworkRxBytes = %d, want 2000000 (bad-rx line skipped)", m.NetworkRxBytes)
	}
}

// TestCollectNetworkFixtureBadTxBytes verifies a non-numeric tx field is skipped.
func TestCollectNetworkFixtureBadTxBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := netDevHeader +
		// Field [8] (9th after colon) is tx_bytes — make it bad.
		"  eth0: 2000000    1000    0    0    0     0          0         0  BADTX     500    0    0    0     0       0          0\n" +
		"  eth1: 5000000    2000    0    0    0     0          0         0  3000000    1000    0    0    0     0       0          0\n"
	mkProcFixture(t, dir, filepath.Join("net", "dev"), content)

	c := collectorWithProc(dir)
	m := &HostMetrics{}
	c.collectNetwork(m)

	// eth0 tx parse fails → skip; eth1 counts.
	if m.NetworkRxBytes != 5000000 {
		t.Errorf("NetworkRxBytes = %d, want 5000000", m.NetworkRxBytes)
	}
	if m.NetworkTxBytes != 3000000 {
		t.Errorf("NetworkTxBytes = %d, want 3000000", m.NetworkTxBytes)
	}
}

// TestCollectNetworkFixtureLineWithoutColon verifies lines without ":" are
// skipped (the len(parts) != 2 branch).
func TestCollectNetworkFixtureLineWithoutColon(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := netDevHeader +
		"  thislinehasnocolon\n" +
		"  eth0: 2000000    1000    0    0    0     0          0         0  1000000     500    0    0    0     0       0          0\n"
	mkProcFixture(t, dir, filepath.Join("net", "dev"), content)

	c := collectorWithProc(dir)
	m := &HostMetrics{}
	c.collectNetwork(m)

	if m.NetworkRxBytes != 2000000 {
		t.Errorf("NetworkRxBytes = %d, want 2000000 (no-colon line skipped)", m.NetworkRxBytes)
	}
}

// ---------------------------------------------------------------------------
// collectUptime tests
// ---------------------------------------------------------------------------

// TestCollectUptimeFixtureValid verifies normal uptime parsing.
func TestCollectUptimeFixtureValid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkProcFixture(t, dir, "uptime", "3661.42 7200.00\n")
	c := collectorWithProc(dir)
	got := c.collectUptime()
	if got != 3661 {
		t.Errorf("collectUptime() = %d, want 3661", got)
	}
}

// TestCollectUptimeFixtureNoFields verifies empty file returns 0.
func TestCollectUptimeFixtureNoFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkProcFixture(t, dir, "uptime", "   \n")
	c := collectorWithProc(dir)
	got := c.collectUptime()
	if got != 0 {
		t.Errorf("collectUptime() empty content = %d, want 0", got)
	}
}

// TestCollectUptimeFixtureBadValue verifies non-numeric uptime returns 0.
func TestCollectUptimeFixtureBadValue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkProcFixture(t, dir, "uptime", "NOTANUMBER 1234.5\n")
	c := collectorWithProc(dir)
	got := c.collectUptime()
	if got != 0 {
		t.Errorf("collectUptime() bad value = %d, want 0", got)
	}
}

// TestCollectUptimeFixtureSingleField verifies a file with only one field parses.
func TestCollectUptimeFixtureSingleField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkProcFixture(t, dir, "uptime", "12345.99\n")
	c := collectorWithProc(dir)
	got := c.collectUptime()
	if got != 12345 {
		t.Errorf("collectUptime() single field = %d, want 12345", got)
	}
}

// ---------------------------------------------------------------------------
// proc() helper test
// ---------------------------------------------------------------------------

// TestProcHelperDefault verifies proc() returns "/proc" when procRoot is empty.
func TestProcHelperDefault(t *testing.T) {
	t.Parallel()
	c := &Collector{}
	if got := c.proc(); got != "/proc" {
		t.Errorf("proc() = %q, want /proc", got)
	}
}

// TestProcHelperOverride verifies proc() returns the custom procRoot.
func TestProcHelperOverride(t *testing.T) {
	t.Parallel()
	c := &Collector{procRoot: "/tmp/fakeprod"}
	if got := c.proc(); got != "/tmp/fakeprod" {
		t.Errorf("proc() = %q, want /tmp/fakeprod", got)
	}
}

// ---------------------------------------------------------------------------
// Full Collect() integration via injected procRoot
// ---------------------------------------------------------------------------

// TestCollectWithFixtureProc exercises the full Collect() path using a
// populated fixture proc directory. Verifies all parsed fields have expected
// values determined by the fixture data.
func TestCollectWithFixtureProc(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	mkProcFixture(t, dir, "stat",
		"cpu  1000 200 300 8000 100 50 25 10\n")
	mkProcFixture(t, dir, "meminfo", `MemTotal:       4096000 kB
MemFree:        1024000 kB
MemAvailable:   2048000 kB
Buffers:         256000 kB
Cached:          512000 kB
`)
	mkProcFixture(t, dir, filepath.Join("net", "dev"),
		netDevHeader+
			"  eth0: 1000000  500  0  0  0  0  0  0  500000  250  0  0  0  0  0  0\n")
	mkProcFixture(t, dir, "uptime", "7200.50 14400.00\n")

	c := &Collector{procRoot: dir, skipDisk: true}
	m, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect() error: %v", err)
	}
	if m == nil {
		t.Fatal("Collect() returned nil")
	}

	// First call → CPUUsage always 0 (no previous sample).
	if m.CPUUsage != 0 {
		t.Errorf("CPUUsage first call = %f, want 0", m.CPUUsage)
	}
	if m.MemoryTotal != uint64(4096000*1024) {
		t.Errorf("MemoryTotal = %d, want %d", m.MemoryTotal, uint64(4096000*1024))
	}
	if m.NetworkRxBytes != 1000000 {
		t.Errorf("NetworkRxBytes = %d, want 1000000", m.NetworkRxBytes)
	}
	if m.NetworkTxBytes != 500000 {
		t.Errorf("NetworkTxBytes = %d, want 500000", m.NetworkTxBytes)
	}
	if m.Uptime != 7200 {
		t.Errorf("Uptime = %d, want 7200", m.Uptime)
	}
}
