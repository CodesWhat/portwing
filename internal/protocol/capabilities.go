// SPDX-License-Identifier: MIT

package protocol

// MergeCapabilities preserves the first occurrence of each case-sensitive
// capability across the supplied groups, without changing the input slices.
func MergeCapabilities(groups ...[]string) []string {
	size := 0
	for _, group := range groups {
		size += len(group)
	}
	merged := make([]string, 0, size)
	seen := make(map[string]struct{}, size)
	for _, group := range groups {
		for _, capability := range group {
			if _, exists := seen[capability]; exists {
				continue
			}
			seen[capability] = struct{}{}
			merged = append(merged, capability)
		}
	}
	return merged
}
