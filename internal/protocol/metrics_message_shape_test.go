package protocol

// Guards against the drift this file is named for: metrics.HostMetrics is the
// struct the edge client and MCP tool actually marshal onto the wire (see
// client.go's sendMetrics and mcp.go's toolHostMetrics, both of which encode
// *metrics.HostMetrics directly rather than building a protocol.MetricsMessage).
// MetricsMessage exists to document that wire shape for readers and for the
// fuzz corpus's decode-only round trip. Nothing else type-checks it against
// HostMetrics, which is exactly how it fell out of sync when HostMetrics grew
// DiskMetricsAvailable/DiskError and MetricsMessage did not.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/metrics"
)

// jsonFieldName returns the field's JSON key from its struct tag, or "" when
// the field is unexported or explicitly ignored (json:"-").
func jsonFieldName(f reflect.StructField) string {
	if f.PkgPath != "" {
		return "" // unexported
	}
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	name := strings.Split(tag, ",")[0]
	if name == "-" {
		return ""
	}
	if name == "" {
		return f.Name
	}
	return name
}

// TestMetricsMessageMirrorsHostMetrics fails loudly when either struct gains a
// JSON field the other doesn't mirror: every json-tagged, exported field on
// either type must have a same-name, same-type counterpart on the other.
func TestMetricsMessageMirrorsHostMetrics(t *testing.T) {
	t.Parallel()

	hostType := reflect.TypeOf(metrics.HostMetrics{})
	msgType := reflect.TypeOf(MetricsMessage{})

	hostFieldsByJSONName := make(map[string]reflect.StructField)
	msgFieldsByJSONName := make(map[string]reflect.StructField)
	for i := 0; i < msgType.NumField(); i++ {
		f := msgType.Field(i)
		name := jsonFieldName(f)
		if name == "" {
			continue
		}
		msgFieldsByJSONName[name] = f
	}

	for i := 0; i < hostType.NumField(); i++ {
		hf := hostType.Field(i)
		name := jsonFieldName(hf)
		if name == "" {
			continue
		}
		hostFieldsByJSONName[name] = hf
		mf, ok := msgFieldsByJSONName[name]
		if !ok {
			t.Errorf("metrics.HostMetrics field %s (json %q) has no counterpart in protocol.MetricsMessage; add it", hf.Name, name)
			continue
		}
		if mf.Type != hf.Type {
			t.Errorf("field %q: metrics.HostMetrics has type %s, protocol.MetricsMessage has type %s", name, hf.Type, mf.Type)
		}
	}

	for name, mf := range msgFieldsByJSONName {
		if _, ok := hostFieldsByJSONName[name]; !ok {
			t.Errorf("protocol.MetricsMessage field %s (json %q) has no counterpart in metrics.HostMetrics; remove it or add it", mf.Name, name)
		}
	}
}

// TestMetricsMessageDiskAvailabilityJSONRoundTrip locks in the exact tag names
// and omitempty semantics for the two fields that previously drifted:
// diskMetricsAvailable always appears on the wire (even when false), and
// diskError is omitted only when empty.
func TestMetricsMessageDiskAvailabilityJSONRoundTrip(t *testing.T) {
	t.Parallel()

	m := MetricsMessage{
		DiskMetricsAvailable: false,
		DiskError:            "statfs /var/lib/docker: no such file or directory",
	}

	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal into map: %v", err)
	}

	avail, ok := decoded["diskMetricsAvailable"]
	if !ok {
		t.Fatal("diskMetricsAvailable key missing from encoded MetricsMessage")
	}
	if avail != false {
		t.Fatalf("diskMetricsAvailable = %v, want false", avail)
	}

	errVal, ok := decoded["diskError"]
	if !ok {
		t.Fatal("diskError key missing from encoded MetricsMessage")
	}
	if errVal != m.DiskError {
		t.Fatalf("diskError = %v, want %q", errVal, m.DiskError)
	}

	// diskError must be omitted entirely when empty (matches HostMetrics'
	// omitempty semantics), so a healthy host doesn't carry a stray "".
	empty := MetricsMessage{DiskMetricsAvailable: true}
	raw, err = json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	decoded = nil
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal empty into map: %v", err)
	}
	if _, ok := decoded["diskError"]; ok {
		t.Fatalf("diskError should be omitted when empty, got it present in %s", raw)
	}

	// And the value must round-trip back into the same struct.
	var back MetricsMessage
	raw, _ = json.Marshal(m)
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal back: %v", err)
	}
	if back != m {
		t.Fatalf("round trip mismatch: got %+v, want %+v", back, m)
	}
}
