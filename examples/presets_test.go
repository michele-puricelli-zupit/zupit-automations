package examples

import (
	"bytes"
	"testing"

	"github.com/zupit-it/zupit-automations/internal/model"
)

func TestPresetsAreIndependentDrafts(t *testing.T) {
	for _, name := range []string{"srt", "cup", "kit", "support"} {
		t.Run(name, func(t *testing.T) {
			f, err := Preset(name)
			if err != nil {
				t.Fatal(err)
			}
			if f.Name == "" || f.ID != 0 || f.SourceID != 0 || f.Enabled || !f.ActivatedAt.IsZero() {
				t.Fatalf("preset must start as an unconfigured draft: %+v", f)
			}
			if f.Definition.Version != 1 || len(f.Definition.Branches) == 0 {
				t.Fatal("preset has no usable definition")
			}
			original := model.JSON(f.Definition)
			if !bytes.Contains(original, []byte("DA_CONFIGURARE")) {
				t.Fatal("preset must mark the configuration still required")
			}
			f.Definition.Filter.Conditions[0].Value = "edited"
			f.Definition.Branches[0].Actions[0].Kind = "edited"
			again, err := Preset(name)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, model.JSON(again.Definition)) {
				t.Fatal("editing one flow changed the next preset")
			}
		})
	}
}

func TestPresetRejectsUnknownNames(t *testing.T) {
	for _, name := range []string{"", "unknown", "SRT", "srt.json", "../examples/srt"} {
		if _, err := Preset(name); err == nil {
			t.Errorf("accepted unsupported preset %q", name)
		}
	}
}
