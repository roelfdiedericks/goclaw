package web

import (
	"fmt"

	"github.com/roelfdiedericks/goclaw/internal/config/forms"
)

// ValidateAllSectionContractsStrict validates all setup/editor section contracts.
// It enforces JSON-pointer config roots and strict JSON-key dotted FormDef paths.
func ValidateAllSectionContractsStrict() error {
	for _, cat := range EditorSections {
		for _, item := range cat.Items {
			if err := forms.ValidateJSONPointer(item.ConfigPath); err != nil {
				return fmt.Errorf(
					"section %q config pointer %q is invalid: %v. expected JSON-key style pointers and dotted field paths",
					item.ID, item.ConfigPath, err,
				)
			}
			if item.Type != SectionTypeFormDef {
				continue
			}
			formDef := GetFormDef(item.ID)
			if formDef == nil {
				return fmt.Errorf("section %q missing form definition", item.ID)
			}
			if err := validateSectionFormBinding(&item, formDef); err != nil {
				return err
			}
		}
	}
	return nil
}
