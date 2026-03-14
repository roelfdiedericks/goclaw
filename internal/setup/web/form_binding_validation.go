package web

import (
	"fmt"
	"reflect"
	"strings"

	appconfig "github.com/roelfdiedericks/goclaw/internal/config"
	"github.com/roelfdiedericks/goclaw/internal/config/forms"
)

// validateSectionFormBinding ensures FormDef field paths are scoped correctly
// for the section's JSON Pointer root.
//
// Rule:
//   - If ConfigPath == "/", fields may reference top-level config keys.
//   - If ConfigPath != "/", fields must be relative to that subtree and must not
//     start with a top-level config key (gateway, agent, llm, ...).
func validateSectionFormBinding(section *SectionItem, def *forms.FormDef) error {
	if section == nil || def == nil || section.Type != SectionTypeFormDef {
		return nil
	}
	sectionRoot, err := sectionRootType(section.ConfigPath)
	if err != nil {
		return err
	}
	if err := forms.ValidateFormDefStrict(sectionRoot, *def); err != nil {
		return fmt.Errorf(
			"invalid form binding for section %q (config pointer %q): %v. expected JSON-key dotted paths that exactly match json tags",
			section.ID, section.ConfigPath, err,
		)
	}
	return nil
}

func sectionRootType(pointer string) (reflect.Type, error) {
	root := reflect.TypeOf(appconfig.Config{})
	if pointer == "/" {
		return root, nil
	}
	path := strings.TrimPrefix(pointer, "/")
	if path == "" {
		return root, nil
	}
	dotted := strings.ReplaceAll(path, "/", ".")
	t, err := forms.ResolveJSONPathTypeStrict(root, dotted)
	if err != nil {
		return nil, fmt.Errorf("invalid section config path %q: %v", pointer, err)
	}
	return t, nil
}
