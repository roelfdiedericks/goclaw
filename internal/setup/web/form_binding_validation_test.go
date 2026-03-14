package web

import (
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/config/forms"
)

func TestAllFormDefSectionsHaveValidBindings(t *testing.T) {
	for _, cat := range EditorSections {
		for i := range cat.Items {
			section := &cat.Items[i]

			if err := forms.ValidateJSONPointer(section.ConfigPath); err != nil {
				t.Fatalf("section %q has invalid config path %q: %v", section.ID, section.ConfigPath, err)
			}

			if section.Type != SectionTypeFormDef {
				continue
			}

			def := GetFormDef(section.ID)
			if def == nil {
				t.Fatalf("section %q is formdef but has no registered FormDef", section.ID)
			}

			if err := validateSectionFormBinding(section, def); err != nil {
				t.Fatalf("section %q binding validation failed: %v", section.ID, err)
			}
		}
	}
}

