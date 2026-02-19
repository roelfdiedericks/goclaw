package forms

import (
	"fmt"
	"reflect"
	"strconv"

	"github.com/charmbracelet/huh"
	"github.com/roelfdiedericks/goclaw/internal/bus"
)

// FormResult holds the result of running a form
type FormResult struct {
	Cancelled bool           // User cancelled/escaped
	Values    map[string]any // Field values by name
}

// RenderForm creates a huh form from a FormDef and populates it with values from the struct
// Returns a function to extract updated values back to the struct
func RenderForm(def FormDef, value any) (*huh.Form, func() error, error) {
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("value must be a struct, got %T", value)
	}

	var groups []*huh.Group
	var bindings []fieldBinding

	for _, section := range def.Sections {
		if section.Nested != nil {
			// Handle nested FormDef - render as separate section
			// For MVP, flatten nested fields into this form
			nestedField := findField(rv, section.Title)
			if nestedField.IsValid() {
				nestedGroups, nestedBindings, err := renderSection(*section.Nested, section.Title, nestedField)
				if err != nil {
					return nil, nil, fmt.Errorf("nested section %s: %w", section.Title, err)
				}
				groups = append(groups, nestedGroups...)
				bindings = append(bindings, nestedBindings...)
			}
			continue
		}

		sectionGroups, sectionBindings, err := renderSectionFields(section, rv)
		if err != nil {
			return nil, nil, err
		}
		groups = append(groups, sectionGroups...)
		bindings = append(bindings, sectionBindings...)
	}

	if len(groups) == 0 {
		return nil, nil, fmt.Errorf("no form fields generated")
	}

	form := huh.NewForm(groups...).WithShowHelp(true)

	// Return function to apply values back to struct
	applyFn := func() error {
		for _, b := range bindings {
			if err := b.Apply(); err != nil {
				return fmt.Errorf("field %s: %w", b.Name, err)
			}
		}
		return nil
	}

	return form, applyFn, nil
}

// renderSection renders a nested struct's FormDef
func renderSection(def FormDef, prefix string, rv reflect.Value) ([]*huh.Group, []fieldBinding, error) {
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	var groups []*huh.Group
	var bindings []fieldBinding

	for _, section := range def.Sections {
		sectionGroups, sectionBindings, err := renderSectionFields(section, rv)
		if err != nil {
			return nil, nil, err
		}
		groups = append(groups, sectionGroups...)
		bindings = append(bindings, sectionBindings...)
	}

	return groups, bindings, nil
}

// renderSectionFields renders fields in a section
func renderSectionFields(section Section, rv reflect.Value) ([]*huh.Group, []fieldBinding, error) {
	var fields []huh.Field
	var bindings []fieldBinding

	for _, fieldDef := range section.Fields {
		field, binding, err := renderField(fieldDef, rv)
		if err != nil {
			return nil, nil, fmt.Errorf("field %s: %w", fieldDef.Name, err)
		}
		if field != nil {
			fields = append(fields, field)
			bindings = append(bindings, binding)
		}
	}

	if len(fields) == 0 {
		return nil, nil, nil
	}

	// Create group with section title
	group := huh.NewGroup(fields...).Title(section.Title)
	if section.Desc != "" {
		group = group.Description(section.Desc)
	}

	return []*huh.Group{group}, bindings, nil
}

// fieldBinding tracks how to apply a form value back to the struct
type fieldBinding struct {
	Name       string
	FieldValue reflect.Value
	FormValue  any // Pointer to form field's bound value
	FieldType  FieldType
}

func (b fieldBinding) Apply() error {
	if !b.FieldValue.CanSet() {
		return fmt.Errorf("cannot set field")
	}

	switch b.FieldType {
	case Toggle:
		if v, ok := b.FormValue.(*bool); ok {
			b.FieldValue.SetBool(*v)
		}
	case Text, Secret, TextArea:
		if v, ok := b.FormValue.(*string); ok {
			b.FieldValue.SetString(*v)
		}
	case Number:
		if v, ok := b.FormValue.(*string); ok {
			return setNumericField(b.FieldValue, *v)
		}
	case Select:
		if v, ok := b.FormValue.(*string); ok {
			b.FieldValue.SetString(*v)
		}
	}
	return nil
}

// setNumericField parses a string and sets it on a numeric field
func setNumericField(rv reflect.Value, s string) error {
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		rv.SetInt(i)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		rv.SetFloat(f)
	default:
		return fmt.Errorf("unsupported numeric type: %v", rv.Kind())
	}
	return nil
}

// renderField creates a huh field from a FieldDef
func renderField(def Field, rv reflect.Value) (huh.Field, fieldBinding, error) {
	fv := findFieldByJSONTag(rv, def.Name)
	if !fv.IsValid() {
		// Try direct name match
		fv = rv.FieldByName(def.Name)
	}
	if !fv.IsValid() {
		return nil, fieldBinding{}, fmt.Errorf("field not found in struct")
	}

	binding := fieldBinding{
		Name:       def.Name,
		FieldValue: fv,
		FieldType:  def.Type,
	}

	var field huh.Field

	switch def.Type {
	case Toggle:
		val := fv.Bool()
		binding.FormValue = &val
		field = huh.NewConfirm().
			Title(def.Title).
			Description(def.Desc).
			Value(&val)

	case Text, Secret:
		val := fmt.Sprintf("%v", fv.Interface())
		binding.FormValue = &val
		input := huh.NewInput().
			Title(def.Title).
			Description(def.Desc).
			Value(&val)
		if def.Type == Secret {
			input = input.EchoMode(huh.EchoModePassword)
		}
		field = input

	case Number:
		val := fmt.Sprintf("%v", fv.Interface())
		binding.FormValue = &val
		input := huh.NewInput().
			Title(def.Title).
			Description(def.Desc).
			Value(&val)
		// TODO: Add validation for numeric range
		field = input

	case Select:
		val := fmt.Sprintf("%v", fv.Interface())
		binding.FormValue = &val
		options := make([]huh.Option[string], len(def.Options))
		for i, opt := range def.Options {
			options[i] = huh.NewOption(opt.Label, opt.Value)
		}
		field = huh.NewSelect[string]().
			Title(def.Title).
			Description(def.Desc).
			Options(options...).
			Value(&val)

	case TextArea:
		val := fmt.Sprintf("%v", fv.Interface())
		binding.FormValue = &val
		field = huh.NewText().
			Title(def.Title).
			Description(def.Desc).
			Value(&val)

	default:
		return nil, fieldBinding{}, fmt.Errorf("unsupported field type: %v", def.Type)
	}

	return field, binding, nil
}

// findField finds a struct field by name (case-insensitive)
func findField(rv reflect.Value, name string) reflect.Value {
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	return rv.FieldByName(name)
}

// findFieldByJSONTag finds a struct field by its json tag
func findFieldByJSONTag(rv reflect.Value, jsonName string) reflect.Value {
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	rt := rv.Type()

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		tag := field.Tag.Get("json")
		// Handle "name,omitempty" format
		if idx := len(tag); idx > 0 {
			for j, c := range tag {
				if c == ',' {
					tag = tag[:j]
					break
				}
			}
		}
		if tag == jsonName {
			return rv.Field(i)
		}
	}
	return reflect.Value{}
}

// ExecuteAction sends a command through the bus
func ExecuteAction(component string, action ActionDef, payload any) bus.CommandResult {
	return bus.SendCommandWithSource(component, action.Name, payload, "tui", "")
}
