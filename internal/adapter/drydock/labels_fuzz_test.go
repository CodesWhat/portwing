package drydock

import (
	"testing"
)

// FuzzParseLabels verifies that ParseLabels never panics on arbitrary label
// maps (constructed from fuzz-generated key/value pairs) and that the result
// fields are always valid strings (never produce a panic on access).
func FuzzParseLabels(f *testing.F) {
	// Seed: well-formed label maps as flat key\x00value\x00... pairs.
	// The fuzzer receives a single string that we split on \x00 to form pairs.
	f.Add(LabelDisplayName + "\x00MyService\x00" + LabelWatch + "\x00docker")
	f.Add(LabelTagInclude + "\x00^v\\d+$\x00" + LabelTagExclude + "\x00beta")
	f.Add(LabelDisplayIcon + "\x00mdi-server\x00" + LabelTagTransform + "\x00s/^v//")
	// Hostile seeds.
	f.Add("")
	f.Add("\x00")
	f.Add("key\x00")     // key with no value
	f.Add("\x00value")   // empty key
	f.Add("k\x00v\x00k") // odd number of tokens (unpaired key)

	f.Fuzz(func(t *testing.T, input string) {
		// Build a label map from the fuzz input: split on \x00 into pairs.
		parts := splitOnNull(input)
		labels := make(map[string]string, len(parts)/2)
		for i := 0; i+1 < len(parts); i += 2 {
			labels[parts[i]] = parts[i+1]
		}

		// Must never panic.
		result := ParseLabels(labels)

		// All fields are plain strings; just access them to ensure no panic.
		_ = result.DisplayName
		_ = result.DisplayIcon
		_ = result.IncludeTags
		_ = result.ExcludeTags
		_ = result.TransformTags
		_ = result.Watcher
	})
}

// splitOnNull splits s on the NUL byte.
func splitOnNull(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
