// SPDX-License-Identifier: MIT

package protocol

// MergeCapabilities preserves the first occurrence of each case-sensitive
// capability across the supplied groups, without changing the input slices.
func MergeCapabilities(groups ...[]string) []string {
	var merged []string
	seen := make(map[string]struct{})
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
