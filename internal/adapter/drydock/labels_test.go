// SPDX-License-Identifier: MIT

package drydock

import (
	"reflect"
	"testing"

	"github.com/codeswhat/portwing/internal/adapter"
)

func TestParseLabels(t *testing.T) {
	t.Parallel()

	labels := map[string]string{
		LabelDisplayName:  "API",
		LabelDisplayIcon:  "mdi-api",
		LabelTagInclude:   "^v\\d+\\.\\d+$",
		LabelTagExclude:   "beta",
		LabelTagTransform: "s/^v//",
		LabelWatch:        "docker",
	}

	got := ParseLabels(labels)
	want := adapter.LabelResult{
		DisplayName:   "API",
		DisplayIcon:   "mdi-api",
		IncludeTags:   "^v\\d+\\.\\d+$",
		ExcludeTags:   "beta",
		TransformTags: "s/^v//",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseLabels() = %+v, want %+v", got, want)
	}
}

func TestParseLabelsWithNilMapReturnsZeroValues(t *testing.T) {
	t.Parallel()

	got := ParseLabels(nil)
	want := adapter.LabelResult{}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseLabels(nil) = %+v, want %+v", got, want)
	}
}

func TestWatchLabelDoesNotSelectWatcher(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"true", "false", "docker", "custom", ""} {
		t.Run(value, func(t *testing.T) {
			got := ParseLabels(map[string]string{LabelWatch: value}).Watcher
			if got != "" {
				t.Fatalf("dd.watch=%q selected watcher %q, want default Docker watcher", value, got)
			}
		})
	}
}
