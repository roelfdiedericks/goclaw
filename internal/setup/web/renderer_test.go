package web

import (
	"strings"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/config/forms"
)

func TestRenderFormHTMLUsesDataHooksInsteadOfAlpine(t *testing.T) {
	def := forms.FormDef{
		Title:       "Gateway",
		Description: "Gateway settings",
		Sections: []forms.Section{
			{
				Title:    "General",
				ShowWhen: "gateway.enabled=true",
				Fields: []forms.Field{
					{Name: "enabled", Title: "Enabled", Type: forms.Toggle},
					{Name: "logFile", Title: "Log File", Type: forms.Text, Placeholder: "~/.goclaw/goclaw.log"},
					{Name: "listen", Title: "Listen", Type: forms.Select, Options: []forms.Option{
						{Label: "Local", Value: "127.0.0.1"},
						{Label: "All", Value: "0.0.0.0"},
					}},
				},
			},
		},
	}

	html, err := RenderFormHTML(def, "formData")
	if err != nil {
		t.Fatalf("RenderFormHTML returned error: %v", err)
	}

	got := string(html)

	if !strings.Contains(got, `data-showwhen="gateway.enabled=true"`) {
		t.Fatalf("expected section showwhen hook, got:\n%s", got)
	}
	if !strings.Contains(got, `data-bind="enabled"`) {
		t.Fatalf("expected toggle data-bind hook, got:\n%s", got)
	}
	if !strings.Contains(got, `data-bind="logFile"`) {
		t.Fatalf("expected text field data-bind hook, got:\n%s", got)
	}
	if !strings.Contains(got, `placeholder="~/.goclaw/goclaw.log"`) {
		t.Fatalf("expected placeholder attribute, got:\n%s", got)
	}
	if !strings.Contains(got, `data-bind="listen"`) {
		t.Fatalf("expected select data-bind hook, got:\n%s", got)
	}
	if !strings.Contains(got, `data-field-error="logFile"`) {
		t.Fatalf("expected field error hook, got:\n%s", got)
	}

	for _, forbidden := range []string{
		`x-model`,
		`x-show`,
		`x-if`,
		`x-for`,
		`x-html`,
		`x-collapse`,
		`@click`,
		`:class=`,
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("expected no Alpine directive %q in rendered form HTML:\n%s", forbidden, got)
		}
	}
}

func TestRenderFormHTMLRendersWidgetPlaceholders(t *testing.T) {
	def := forms.FormDef{
		Sections: []forms.Section{
			{
				FieldName: "llm",
				Fields: []forms.Field{
					{Name: "agent.models", Title: "Agent Models", Type: forms.ModelChain, Purpose: "agent"},
					{Name: "providers", Title: "Providers", Type: forms.ProviderList},
				},
			},
			{
				Fields: []forms.Field{
					{Name: "roles", Title: "Roles", Type: forms.RolesList},
				},
			},
		},
	}

	html, err := RenderFormHTML(def, "formData")
	if err != nil {
		t.Fatalf("RenderFormHTML returned error: %v", err)
	}

	got := string(html)

	if !strings.Contains(got, `data-widget="model-chain"`) {
		t.Fatalf("expected model-chain widget placeholder, got:\n%s", got)
	}
	if !strings.Contains(got, `data-field-path="llm.agent.models"`) {
		t.Fatalf("expected model-chain field path, got:\n%s", got)
	}
	if !strings.Contains(got, `data-purpose="agent"`) {
		t.Fatalf("expected model-chain purpose, got:\n%s", got)
	}
	if !strings.Contains(got, `data-widget="provider-list"`) {
		t.Fatalf("expected provider-list widget placeholder, got:\n%s", got)
	}
	if !strings.Contains(got, `data-field-path="llm.providers"`) {
		t.Fatalf("expected provider-list field path, got:\n%s", got)
	}
	if !strings.Contains(got, `data-widget="roles-list"`) {
		t.Fatalf("expected roles-list widget placeholder, got:\n%s", got)
	}
	if !strings.Contains(got, `data-field-path="roles"`) {
		t.Fatalf("expected roles-list field path, got:\n%s", got)
	}
}

func TestRenderFormHTMLCollapsedSectionUsesBootstrapCollapse(t *testing.T) {
	def := forms.FormDef{
		Sections: []forms.Section{
			{
				Title:     "Advanced",
				Collapsed: true,
				Fields: []forms.Field{
					{Name: "timeout", Title: "Timeout", Type: forms.Number},
				},
			},
		},
	}

	html, err := RenderFormHTML(def, "formData")
	if err != nil {
		t.Fatalf("RenderFormHTML returned error: %v", err)
	}

	got := string(html)
	if !strings.Contains(got, `data-bs-toggle="collapse"`) {
		t.Fatalf("expected bootstrap collapse toggle, got:\n%s", got)
	}
	if !strings.Contains(got, `class="card-body collapse"`) {
		t.Fatalf("expected collapsed card body, got:\n%s", got)
	}
}

func TestRenderFormHTMLSliderFieldRendersRangeInput(t *testing.T) {
	def := forms.FormDef{
		Sections: []forms.Section{
			{
				Fields: []forms.Field{
					{Name: "quota", Title: "Quota", Type: forms.Slider, Min: 0.5, Max: 10, Step: 0.5, Scale: 1073741824, Unit: "GB"},
				},
			},
		},
	}

	html, err := RenderFormHTML(def, "formData")
	if err != nil {
		t.Fatalf("RenderFormHTML returned error: %v", err)
	}

	got := string(html)
	if !strings.Contains(got, `type="range"`) {
		t.Fatalf("expected range input, got:\n%s", got)
	}
	if !strings.Contains(got, `class="form-range js-bound-field js-slider-field"`) {
		t.Fatalf("expected slider classes, got:\n%s", got)
	}
	if !strings.Contains(got, `data-scale="1.073741824e+09"`) {
		t.Fatalf("expected scale attribute, got:\n%s", got)
	}
	if !strings.Contains(got, `data-unit="GB"`) {
		t.Fatalf("expected unit attribute, got:\n%s", got)
	}
}

func TestRenderFormHTMLRendersFormActionsExceptApply(t *testing.T) {
	def := forms.FormDef{
		Actions: []forms.ActionDef{
			{Name: "stats", Label: "Refresh Usage", Desc: "Show latest usage"},
			{Name: "clean", Label: "Clean Now", Confirm: "Run cleanup?"},
			{Name: "apply", Label: "Apply"},
		},
	}

	html, err := RenderFormHTML(def, "formData")
	if err != nil {
		t.Fatalf("RenderFormHTML returned error: %v", err)
	}

	got := string(html)
	if !strings.Contains(got, `class="btn btn-outline-secondary js-form-action"`) {
		t.Fatalf("expected form action button markup, got:\n%s", got)
	}
	if !strings.Contains(got, `data-action-name="stats"`) || !strings.Contains(got, `Refresh Usage`) {
		t.Fatalf("expected stats action button, got:\n%s", got)
	}
	if !strings.Contains(got, `data-action-name="clean"`) || !strings.Contains(got, `data-action-confirm="Run cleanup?"`) {
		t.Fatalf("expected clean action button with confirm, got:\n%s", got)
	}
	if strings.Contains(got, `data-action-name="apply"`) {
		t.Fatalf("did not expect apply action to be rendered in web form, got:\n%s", got)
	}
}
