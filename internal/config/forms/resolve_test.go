package forms

import (
	"reflect"
	"testing"
)

func TestResolveJSONPathTypeStrict(t *testing.T) {
	type Inner struct {
		APIKey string `json:"apiKey"`
	}
	type Root struct {
		Inner Inner `json:"inner"`
	}

	got, err := ResolveJSONPathTypeStrict(reflect.TypeOf(Root{}), "inner.apiKey")
	if err != nil {
		t.Fatalf("ResolveJSONPathTypeStrict error: %v", err)
	}
	if got.Kind() != reflect.String {
		t.Fatalf("expected string, got %v", got.Kind())
	}
	if _, err := ResolveJSONPathTypeStrict(reflect.TypeOf(Root{}), "Inner.APIKey"); err == nil {
		t.Fatalf("expected strict resolver to reject non-json-key path")
	}
}

func TestResolveJSONPathStrict(t *testing.T) {
	type Inner struct {
		APIKey string `json:"apiKey"`
	}
	type Root struct {
		Inner Inner `json:"inner"`
	}
	v := reflect.ValueOf(Root{Inner: Inner{APIKey: "x"}})
	f, err := ResolveJSONPathStrict(v, "inner.apiKey")
	if err != nil {
		t.Fatalf("ResolveJSONPathStrict error: %v", err)
	}
	if !f.IsValid() || f.Kind() != reflect.String || f.String() != "x" {
		t.Fatalf("unexpected resolved value")
	}
}

func TestJSONPointerGetSet(t *testing.T) {
	root := map[string]interface{}{
		"llm": map[string]interface{}{
			"providers": map[string]interface{}{
				"xai": map[string]interface{}{"apiKey": "abc"},
			},
		},
	}

	v, err := JSONPointerGet(root, "/llm/providers")
	if err != nil {
		t.Fatalf("JSONPointerGet error: %v", err)
	}
	if _, ok := v.(map[string]interface{}); !ok {
		t.Fatalf("expected object from JSONPointerGet")
	}

	err = JSONPointerSet(root, "/llm/providers/openai", map[string]interface{}{"apiKey": "k"})
	if err != nil {
		t.Fatalf("JSONPointerSet error: %v", err)
	}

	v2, err := JSONPointerGet(root, "/llm/providers/openai")
	if err != nil {
		t.Fatalf("JSONPointerGet error: %v", err)
	}
	m, ok := v2.(map[string]interface{})
	if !ok || m["apiKey"] != "k" {
		t.Fatalf("unexpected value at /llm/providers/openai: %#v", v2)
	}
}

func TestValidateJSONPointer(t *testing.T) {
	valid := []string{"/", "/llm", "/llm/providers", "/roles"}
	for _, p := range valid {
		if err := ValidateJSONPointer(p); err != nil {
			t.Fatalf("expected valid pointer %q, got err: %v", p, err)
		}
	}

	invalid := []string{"", "llm", "//llm", "/llm//providers", "/llm/~2bad"}
	for _, p := range invalid {
		if err := ValidateJSONPointer(p); err == nil {
			t.Fatalf("expected invalid pointer %q", p)
		}
	}
}
