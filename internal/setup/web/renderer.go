// Package web provides browser-based setup wizard and configuration editor.
package web

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/roelfdiedericks/goclaw/internal/config/forms"
)

// RenderFormHTML converts a FormDef into Bootstrap-oriented HTML with data hooks.
// The returned markup is hydrated by the setup jQuery runtime.
func RenderFormHTML(def forms.FormDef, _ string) (template.HTML, error) {
	var sb strings.Builder
	if def.Title != "" {
		sb.WriteString(fmt.Sprintf(`<h5 class="mb-3">%s</h5>`+"\n", template.HTMLEscapeString(def.Title)))
	}
	if def.Description != "" {
		sb.WriteString(fmt.Sprintf(`<p class="text-muted">%s</p>`+"\n", template.HTMLEscapeString(def.Description)))
	}

	for _, sec := range def.Sections {
		if err := renderSection(&sb, sec, ""); err != nil {
			return "", err
		}
	}

	// #nosec G203 -- This markup is assembled from fixed HTML plus escaped FormDef text.
	return template.HTML(sb.String()), nil
}

func renderSection(sb *strings.Builder, sec forms.Section, prefix string) error {
	nestedPrefix := prefix
	if sec.FieldName != "" {
		nestedPrefix = joinPath(prefix, sec.FieldName)
	}

	cardID := sectionID(nestedPrefix, sec.Title)
	bodyID := cardID + "_body"

	sb.WriteString(`<div class="card mb-3 js-form-section"`)
	if sec.ShowWhen != "" {
		sb.WriteString(fmt.Sprintf(` data-showwhen="%s"`, template.HTMLEscapeString(sec.ShowWhen)))
	}
	sb.WriteString(">\n")

	if sec.Title != "" {
		sb.WriteString(`  <div class="card-header">`)
		if sec.Collapsed {
			sb.WriteString(fmt.Sprintf(`<button type="button" class="btn btn-link text-decoration-none p-0 w-100 text-start" data-bs-toggle="collapse" data-bs-target="#%s" aria-expanded="false">`, bodyID))
			sb.WriteString(template.HTMLEscapeString(sec.Title))
			sb.WriteString(` <i class="bi bi-chevron-down float-end"></i></button>`)
		} else {
			sb.WriteString(template.HTMLEscapeString(sec.Title))
		}
		sb.WriteString("</div>\n")
	}

	sb.WriteString(fmt.Sprintf(`  <div class="card-body%s" id="%s">`+"\n", collapsedClass(sec.Collapsed), bodyID))
	if sec.Desc != "" {
		sb.WriteString(fmt.Sprintf(`    <p class="text-muted small mb-3">%s</p>`+"\n", template.HTMLEscapeString(sec.Desc)))
	}

	for _, field := range sec.Fields {
		if err := renderField(sb, field, nestedPrefix); err != nil {
			return err
		}
	}

	if sec.Nested != nil {
		for _, nested := range sec.Nested.Sections {
			if err := renderSection(sb, nested, nestedPrefix); err != nil {
				return err
			}
		}
	}

	sb.WriteString("  </div>\n")
	sb.WriteString("</div>\n")
	return nil
}

func renderField(sb *strings.Builder, field forms.Field, prefix string) error {
	fieldPath := joinPath(prefix, field.Name)
	fieldKey := fieldPath
	inputID := fieldID(fieldPath)

	switch field.Type {
	case forms.Toggle:
		renderToggle(sb, field, inputID, fieldKey, fieldPath)
		return nil
	case forms.ModelChain:
		renderModelChain(sb, field, fieldPath, fieldKey)
		return nil
	case forms.ProviderList:
		renderProviderList(sb, field, fieldPath, fieldKey)
		return nil
	case forms.RolesList:
		renderRolesList(sb, field, fieldPath, fieldKey)
		return nil
	}

	sb.WriteString(`    <div class="mb-3 js-field">` + "\n")

	if field.Title != "" {
		sb.WriteString(fmt.Sprintf(`      <label class="form-label" for="%s">%s</label>`+"\n",
			inputID, template.HTMLEscapeString(field.Title)))
	}

	switch field.Type {
	case forms.Text:
		renderText(sb, field, inputID, fieldKey, fieldPath)
	case forms.Secret:
		renderSecret(sb, field, inputID, fieldKey, fieldPath)
	case forms.Number:
		renderNumber(sb, field, inputID, fieldKey, fieldPath)
	case forms.Select:
		renderSelect(sb, field, inputID, fieldKey, fieldPath)
	case forms.TextArea:
		renderTextArea(sb, field, inputID, fieldKey, fieldPath)
	case forms.StringList:
		renderStringList(sb, field, inputID, fieldKey, fieldPath)
	default:
		renderText(sb, field, inputID, fieldKey, fieldPath)
	}

	if field.Desc != "" {
		sb.WriteString(fmt.Sprintf(`      <div class="form-text">%s</div>`+"\n", template.HTMLEscapeString(field.Desc)))
	}
	sb.WriteString("    </div>\n")
	return nil
}

func renderToggle(sb *strings.Builder, field forms.Field, inputID, fieldKey, fieldPath string) {
	sb.WriteString(`    <div class="mb-3 form-check form-switch js-field">` + "\n")
	sb.WriteString(fmt.Sprintf(`      <input type="checkbox" class="form-check-input js-bound-field" id="%s" data-bind="%s" data-bind-type="boolean">`+"\n",
		inputID, template.HTMLEscapeString(fieldPath)))
	if field.Title != "" {
		sb.WriteString(fmt.Sprintf(`      <label class="form-check-label" for="%s">%s</label>`+"\n",
			inputID, template.HTMLEscapeString(field.Title)))
	}
	renderFieldError(sb, fieldKey)
	if field.Desc != "" {
		sb.WriteString(fmt.Sprintf(`      <div class="form-text">%s</div>`+"\n", template.HTMLEscapeString(field.Desc)))
	}
	sb.WriteString("    </div>\n")
}

func renderText(sb *strings.Builder, field forms.Field, inputID, fieldKey, fieldPath string) {
	sb.WriteString(fmt.Sprintf(`      <input type="text" class="form-control js-bound-field" id="%s" data-bind="%s"`,
		inputID, template.HTMLEscapeString(fieldPath)))
	if field.Placeholder != "" {
		sb.WriteString(fmt.Sprintf(` placeholder="%s"`, template.HTMLEscapeString(field.Placeholder)))
	}
	sb.WriteString(">\n")
	renderFieldError(sb, fieldKey)
}

func renderSecret(sb *strings.Builder, field forms.Field, inputID, fieldKey, fieldPath string) {
	sb.WriteString(fmt.Sprintf(`      <input type="password" class="form-control js-bound-field" id="%s" data-bind="%s" autocomplete="off" data-lpignore="true" data-form-type="other"`,
		inputID, template.HTMLEscapeString(fieldPath)))
	if field.Placeholder != "" {
		sb.WriteString(fmt.Sprintf(` placeholder="%s"`, template.HTMLEscapeString(field.Placeholder)))
	}
	sb.WriteString(">\n")
	renderFieldError(sb, fieldKey)
}

func renderNumber(sb *strings.Builder, field forms.Field, inputID, fieldKey, fieldPath string) {
	sb.WriteString(fmt.Sprintf(`      <input type="number" class="form-control js-bound-field" id="%s" data-bind="%s" data-bind-type="number"`,
		inputID, template.HTMLEscapeString(fieldPath)))
	if field.Min != 0 {
		sb.WriteString(fmt.Sprintf(` min="%v"`, field.Min))
	}
	if field.Max != 0 {
		sb.WriteString(fmt.Sprintf(` max="%v"`, field.Max))
	}
	if field.Step != 0 {
		sb.WriteString(fmt.Sprintf(` step="%v"`, field.Step))
	}
	if field.Placeholder != "" {
		sb.WriteString(fmt.Sprintf(` placeholder="%s"`, template.HTMLEscapeString(field.Placeholder)))
	}
	sb.WriteString(">\n")
	renderFieldError(sb, fieldKey)
}

func renderSelect(sb *strings.Builder, field forms.Field, inputID, fieldKey, fieldPath string) {
	sb.WriteString(fmt.Sprintf(`      <select class="form-select js-bound-field" id="%s" data-bind="%s">`+"\n",
		inputID, template.HTMLEscapeString(fieldPath)))
	sb.WriteString(`        <option value="">Select...</option>` + "\n")
	for _, opt := range field.Options {
		sb.WriteString(fmt.Sprintf(`        <option value="%s">%s</option>`+"\n",
			template.HTMLEscapeString(opt.Value),
			template.HTMLEscapeString(opt.Label)))
	}
	sb.WriteString("      </select>\n")
	renderFieldError(sb, fieldKey)
}

func renderTextArea(sb *strings.Builder, field forms.Field, inputID, fieldKey, fieldPath string) {
	sb.WriteString(fmt.Sprintf(`      <textarea class="form-control js-bound-field" id="%s" data-bind="%s" rows="4"`,
		inputID, template.HTMLEscapeString(fieldPath)))
	if field.Placeholder != "" {
		sb.WriteString(fmt.Sprintf(` placeholder="%s"`, template.HTMLEscapeString(field.Placeholder)))
	}
	sb.WriteString("></textarea>\n")
	renderFieldError(sb, fieldKey)
}

func renderStringList(sb *strings.Builder, field forms.Field, inputID, fieldKey, fieldPath string) {
	sb.WriteString(fmt.Sprintf(`      <input type="text" class="form-control js-bound-field" id="%s" data-bind="%s" data-bind-type="string-list"`,
		inputID, template.HTMLEscapeString(fieldPath)))
	if field.Placeholder != "" {
		sb.WriteString(fmt.Sprintf(` placeholder="%s"`, template.HTMLEscapeString(field.Placeholder)))
	}
	sb.WriteString(">\n")
	renderFieldError(sb, fieldKey)
	sb.WriteString(`      <div class="form-text">Comma-separated values</div>` + "\n")
}

func renderModelChain(sb *strings.Builder, field forms.Field, fieldPath, fieldKey string) {
	sb.WriteString(`    <div class="mb-3 js-field">` + "\n")
	if field.Title != "" {
		sb.WriteString(fmt.Sprintf(`      <label class="form-label">%s</label>`+"\n", template.HTMLEscapeString(field.Title)))
	}
	sb.WriteString(fmt.Sprintf(`      <div class="js-widget js-model-chain" data-widget="model-chain" data-field-path="%s" data-purpose="%s"></div>`+"\n",
		template.HTMLEscapeString(fieldPath), template.HTMLEscapeString(field.Purpose)))
	renderFieldError(sb, fieldKey)
	if field.Desc != "" {
		sb.WriteString(fmt.Sprintf(`      <div class="form-text">%s</div>`+"\n", template.HTMLEscapeString(field.Desc)))
	}
	sb.WriteString("    </div>\n")
}

func renderProviderList(sb *strings.Builder, field forms.Field, fieldPath, fieldKey string) {
	sb.WriteString(`    <div class="mb-3 js-field">` + "\n")
	if field.Title != "" {
		sb.WriteString(fmt.Sprintf(`      <label class="form-label">%s</label>`+"\n", template.HTMLEscapeString(field.Title)))
	}
	sb.WriteString(fmt.Sprintf(`      <div class="js-widget js-provider-list" data-widget="provider-list" data-field-path="%s"></div>`+"\n",
		template.HTMLEscapeString(fieldPath)))
	renderFieldError(sb, fieldKey)
	if field.Desc != "" {
		sb.WriteString(fmt.Sprintf(`      <div class="form-text">%s</div>`+"\n", template.HTMLEscapeString(field.Desc)))
	}
	sb.WriteString("    </div>\n")
}

func renderRolesList(sb *strings.Builder, field forms.Field, fieldPath, fieldKey string) {
	sb.WriteString(`    <div class="mb-3 js-field">` + "\n")
	if field.Title != "" {
		sb.WriteString(fmt.Sprintf(`      <label class="form-label">%s</label>`+"\n", template.HTMLEscapeString(field.Title)))
	}
	sb.WriteString(fmt.Sprintf(`      <div class="js-widget js-roles-list" data-widget="roles-list" data-field-path="%s"></div>`+"\n",
		template.HTMLEscapeString(fieldPath)))
	renderFieldError(sb, fieldKey)
	if field.Desc != "" {
		sb.WriteString(fmt.Sprintf(`      <div class="form-text">%s</div>`+"\n", template.HTMLEscapeString(field.Desc)))
	}
	sb.WriteString("    </div>\n")
}

func renderFieldError(sb *strings.Builder, fieldName string) {
	sb.WriteString(fmt.Sprintf(`      <div class="invalid-feedback d-none" data-field-error="%s"></div>`+"\n",
		template.HTMLEscapeString(fieldName)))
}

func joinPath(prefix, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return prefix
	}
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func fieldID(fieldPath string) string {
	return "field_" + strings.ReplaceAll(fieldPath, ".", "__")
}

func sectionID(prefix, title string) string {
	base := strings.TrimSpace(prefix)
	if base == "" {
		base = strings.TrimSpace(title)
	}
	if base == "" {
		base = "section"
	}
	base = strings.ReplaceAll(base, " ", "_")
	return "section_" + strings.ReplaceAll(base, ".", "__")
}

func collapsedClass(collapsed bool) string {
	if collapsed {
		return " collapse"
	}
	return ""
}
