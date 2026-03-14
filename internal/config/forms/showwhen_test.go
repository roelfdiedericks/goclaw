package forms

import (
	"reflect"
	"testing"
)

func TestParseShowWhen(t *testing.T) {
	clauses, err := ParseShowWhen("gateway.enabled=true,agent.name!=GoClaw")
	if err != nil {
		t.Fatalf("ParseShowWhen error: %v", err)
	}
	if len(clauses) != 2 {
		t.Fatalf("expected 2 clauses, got %d", len(clauses))
	}
	if clauses[0].FieldPath != "gateway.enabled" || clauses[0].Operator != showWhenEq || clauses[0].Value != "true" {
		t.Fatalf("unexpected first clause: %#v", clauses[0])
	}
	if clauses[1].FieldPath != "agent.name" || clauses[1].Operator != showWhenNeq || clauses[1].Value != "GoClaw" {
		t.Fatalf("unexpected second clause: %#v", clauses[1])
	}
}

func TestEvaluateShowWhen(t *testing.T) {
	type Gateway struct {
		Enabled bool `json:"enabled"`
	}
	type Agent struct {
		Name string `json:"name"`
	}
	type Cfg struct {
		Gateway Gateway `json:"gateway"`
		Agent   Agent   `json:"agent"`
	}

	rv := reflect.ValueOf(Cfg{
		Gateway: Gateway{Enabled: true},
		Agent:   Agent{Name: "GoClaw"},
	})

	if !EvaluateShowWhen("gateway.enabled=true", rv) {
		t.Fatalf("expected true for equality condition")
	}
	if EvaluateShowWhen("gateway.enabled=false", rv) {
		t.Fatalf("expected false for mismatch condition")
	}
	if !EvaluateShowWhen("gateway.enabled=false,agent.name=GoClaw", rv) {
		t.Fatalf("expected true for OR condition")
	}
	if EvaluateShowWhen("agent.name!=GoClaw", rv) {
		t.Fatalf("expected false for inequality mismatch")
	}
}

func TestCompileShowWhenToAlpine(t *testing.T) {
	expr := CompileShowWhenToAlpine("gateway.enabled=true,agent.name!=GoClaw", "formData")
	want := "(formData.gateway.enabled || formData.agent.name !== 'GoClaw')"
	if expr != want {
		t.Fatalf("CompileShowWhenToAlpine mismatch:\n got: %q\nwant: %q", expr, want)
	}
}

func TestValidateShowWhenStrict(t *testing.T) {
	type Cfg struct {
		Gateway struct {
			Enabled bool `json:"enabled"`
		} `json:"gateway"`
	}
	if err := ValidateShowWhenStrict(reflect.TypeOf(Cfg{}), "gateway.enabled=true"); err != nil {
		t.Fatalf("expected valid showwhen, got %v", err)
	}
	if err := ValidateShowWhenStrict(reflect.TypeOf(Cfg{}), "Gateway.Enabled=true"); err == nil {
		t.Fatalf("expected strict rejection for non-json-key path")
	}
}
