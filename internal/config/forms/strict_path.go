package forms

import (
	"fmt"
	"reflect"
	"strings"
)

func splitStrictPath(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}
	parts := strings.Split(path, ".")
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("invalid path %q: empty segment", path)
		}
	}
	return parts, nil
}

func jsonTagName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if idx := strings.Index(tag, ","); idx != -1 {
		tag = tag[:idx]
	}
	return tag
}

func findStructFieldByJSONTag(rt reflect.Type, jsonKey string) (reflect.StructField, bool) {
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.PkgPath != "" {
			continue
		}
		if jsonTagName(f) == jsonKey {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// ResolveJSONPathTypeStrict resolves a dotted JSON-key path against a schema type.
// Resolution is strict: struct segments must exactly match json tags.
func ResolveJSONPathTypeStrict(root reflect.Type, path string) (reflect.Type, error) {
	parts, err := splitStrictPath(path)
	if err != nil {
		return nil, err
	}
	current := root
	for _, part := range parts {
		for current.Kind() == reflect.Ptr {
			current = current.Elem()
		}
		switch current.Kind() {
		case reflect.Struct:
			f, ok := findStructFieldByJSONTag(current, part)
			if !ok {
				return nil, fmt.Errorf("segment %q does not match json tag in %s", part, current.String())
			}
			current = f.Type
		case reflect.Map:
			if current.Key().Kind() != reflect.String {
				return nil, fmt.Errorf("segment %q traverses non-string map key type %s", part, current.Key().String())
			}
			current = current.Elem()
		default:
			return nil, fmt.Errorf("segment %q cannot traverse %s", part, current.Kind())
		}
	}
	return current, nil
}

// ResolveJSONPathStrict resolves a dotted JSON-key path against a runtime value.
// Resolution is strict: struct segments must exactly match json tags.
func ResolveJSONPathStrict(root reflect.Value, path string) (reflect.Value, error) {
	parts, err := splitStrictPath(path)
	if err != nil {
		return reflect.Value{}, err
	}
	current := root
	for _, part := range parts {
		for current.Kind() == reflect.Ptr {
			if current.IsNil() {
				return reflect.Value{}, fmt.Errorf("segment %q traverses nil pointer", part)
			}
			current = current.Elem()
		}

		switch current.Kind() {
		case reflect.Struct:
			f, ok := findStructFieldByJSONTag(current.Type(), part)
			if !ok {
				return reflect.Value{}, fmt.Errorf("segment %q does not match json tag in %s", part, current.Type().String())
			}
			current = current.FieldByIndex(f.Index)
		case reflect.Map:
			if current.Type().Key().Kind() != reflect.String {
				return reflect.Value{}, fmt.Errorf("segment %q traverses non-string map key type %s", part, current.Type().Key().String())
			}
			mv := current.MapIndex(reflect.ValueOf(part))
			if !mv.IsValid() {
				return reflect.Value{}, fmt.Errorf("segment %q not found in map", part)
			}
			current = mv
		default:
			return reflect.Value{}, fmt.Errorf("segment %q cannot traverse %s", part, current.Kind())
		}
	}
	return current, nil
}

func joinStrictPath(prefix, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return strings.TrimSpace(prefix)
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func validateSectionsStrict(root reflect.Type, sections []Section, prefix string) error {
	for _, sec := range sections {
		secPrefix := prefix
		if sec.FieldName != "" {
			secPrefix = joinStrictPath(prefix, sec.FieldName)
		}

		// ShowWhen paths are evaluated against the form root contract.
		if err := ValidateShowWhenStrict(root, sec.ShowWhen); err != nil {
			return err
		}

		for _, f := range sec.Fields {
			fp := joinStrictPath(secPrefix, f.Name)
			if strings.TrimSpace(fp) == "" {
				continue
			}
			if _, err := ResolveJSONPathTypeStrict(root, fp); err != nil {
				return fmt.Errorf("field %q: %w", fp, err)
			}
		}
		if sec.Nested != nil {
			if err := validateSectionsStrict(root, sec.Nested.Sections, secPrefix); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateFormDefStrict validates that all FormDef field paths resolve via exact JSON tags.
func ValidateFormDefStrict(root reflect.Type, def FormDef) error {
	if root == nil {
		return fmt.Errorf("nil root type")
	}
	return validateSectionsStrict(root, def.Sections, "")
}

// ValidateShowWhenStrict validates ShowWhen field-path clauses against exact JSON tags.
func ValidateShowWhenStrict(root reflect.Type, expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}
	clauses, err := ParseShowWhen(expr)
	if err != nil {
		return err
	}
	for _, c := range clauses {
		if _, err := ResolveJSONPathTypeStrict(root, c.FieldPath); err != nil {
			return fmt.Errorf("showWhen %q path %q: %w", expr, c.FieldPath, err)
		}
	}
	return nil
}
