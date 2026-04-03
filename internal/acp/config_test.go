package acp

import (
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/config/forms"
)

func TestConfigFormDefUsesCursorModelSubtree(t *testing.T) {
	def := ConfigFormDef()
	if len(def.Sections) < 2 {
		t.Fatalf("expected ACP form sections, got %d", len(def.Sections))
	}

	var foundCursorModel bool
	var foundDefaultDriver bool
	for _, section := range def.Sections {
		for _, field := range section.Fields {
			if field.Name == "defaultDriver" {
				foundDefaultDriver = true
				if field.Type != forms.Select {
					t.Fatalf("expected defaultDriver to be Select, got %v", field.Type)
				}
			}
			if field.Name == "drivers.cursor.model" {
				foundCursorModel = true
				if field.Type != forms.SelectWithCustom {
					t.Fatalf("expected SelectWithCustom field, got %v", field.Type)
				}
				if len(field.Options) == 0 {
					t.Fatalf("expected known Cursor model options")
				}
			}
		}
	}
	if !foundDefaultDriver {
		t.Fatalf("expected defaultDriver field in ACP form")
	}
	if !foundCursorModel {
		t.Fatalf("expected drivers.cursor.model field in ACP form")
	}
}

func TestCursorModelFieldOptionsIncludeDefault(t *testing.T) {
	options := cursorModelFieldOptions()
	if len(options) == 0 {
		t.Fatalf("expected Cursor model options")
	}
	for _, option := range options {
		if option.Value == DefaultCursorModel {
			return
		}
	}
	t.Fatalf("expected default model %q in options", DefaultCursorModel)
}
