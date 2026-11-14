// SPDX-License-Identifier: MIT

package protocol

import (
	"slices"
	"testing"
)

func TestMergeCapabilitiesContract(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		groups [][]string
		want   []string
	}{
		{name: "empty"},
		{name: "empty groups", groups: [][]string{nil, {}, nil}},
		{name: "overlap", groups: [][]string{{"core", "events", "core"}, nil, {"events", "custom", "custom"}}, want: []string{"core", "events", "custom"}},
		{name: "exact names", groups: [][]string{{"events", "Events", ""}, {"", "future", "events"}}, want: []string{"events", "Events", "", "future"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := make([][]string, len(tc.groups))
			for i, group := range tc.groups {
				original[i] = slices.Clone(group)
			}
			got := MergeCapabilities(tc.groups...)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got%v, want%v", got, tc.want)
			}
			if len(got) > 0 {
				got[0] = "changed output"
			}
			for i, group := range tc.groups {
				if !slices.Equal(group, original[i]) {
					t.Fatalf("input group%d changed: %v, want%v", i, group, original[i])
				}
			}
		})
	}
}
