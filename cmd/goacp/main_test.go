package main

import (
	"encoding/json"
	"testing"
)

func TestAskQuestionResultSelectedIDText(t *testing.T) {
	client := &spikeClient{askResponse: "selected-id-text"}
	params := json.RawMessage(`{
		"questions": [{
			"options": [{"id":"first-option","label":"First"}]
		}]
	}`)
	got, err := client.askQuestionResult(params)
	if err != nil {
		t.Fatalf("askQuestionResult returned error: %v", err)
	}
	asMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", got)
	}
	if asMap["selectedId"] != "first-option" {
		t.Fatalf("expected selectedId first-option, got %#v", asMap["selectedId"])
	}
	if asMap["text"] == "" {
		t.Fatalf("expected non-empty text field")
	}
}

func TestAskQuestionResultSelectedIDs(t *testing.T) {
	client := &spikeClient{askResponse: "selected-ids"}
	params := json.RawMessage(`{
		"questions": [{
			"allowMultiple": true,
			"options": [
				{"id":"first-option","label":"First"},
				{"id":"second-option","label":"Second"},
				{"id":"third-option","label":"Third"}
			]
		}]
	}`)
	got, err := client.askQuestionResult(params)
	if err != nil {
		t.Fatalf("askQuestionResult returned error: %v", err)
	}
	asMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", got)
	}
	selected, ok := asMap["selectedIds"].([]string)
	if !ok {
		t.Fatalf("expected []string selectedIds, got %#v", asMap["selectedIds"])
	}
	if len(selected) != 2 || selected[0] != "first-option" || selected[1] != "second-option" {
		t.Fatalf("unexpected selectedIds: %#v", selected)
	}
}

func TestCreatePlanResultApprovedFeedback(t *testing.T) {
	client := &spikeClient{planResponse: "approved-feedback"}
	got, err := client.createPlanResult()
	if err != nil {
		t.Fatalf("createPlanResult returned error: %v", err)
	}
	asMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", got)
	}
	if approved, ok := asMap["approved"].(bool); !ok || !approved {
		t.Fatalf("expected approved=true, got %#v", asMap["approved"])
	}
	if asMap["userFeedback"] == "" {
		t.Fatalf("expected non-empty userFeedback")
	}
}
