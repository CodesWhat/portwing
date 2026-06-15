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
		Watcher:       "docker",
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
