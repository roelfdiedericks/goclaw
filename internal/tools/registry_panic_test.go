package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/roelfdiedericks/goclaw/internal/types"
)

type panickingTool struct {
	name string
	val  any
}

func (p *panickingTool) Name() string           { return p.name }
func (p *panickingTool) Description() string    { return "tool that always panics (test fixture)" }
func (p *panickingTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (p *panickingTool) Execute(_ context.Context, _ json.RawMessage) (*types.ToolResult, error) {
	panic(p.val)
}

type okTool struct{ name string }

func (o *okTool) Name() string           { return o.name }
func (o *okTool) Description() string    { return "tool that returns OK (test fixture)" }
func (o *okTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (o *okTool) Execute(_ context.Context, _ json.RawMessage) (*types.ToolResult, error) {
	return types.TextResult("ok"), nil
}

// TestRegistryExecuteRecoversPanic verifies that a panic inside a tool's
// Execute is caught by the registry, converted into a normal IsError result
// AND a non-nil error, and does not crash the test process.
func TestRegistryExecuteRecoversPanic(t *testing.T) {
	r := NewRegistry()
	r.Register(&panickingTool{name: "boom", val: "something went wrong"})

	result, err := r.Execute(context.Background(), "boom", json.RawMessage(`{}`))

	if err == nil {
		t.Fatalf("expected non-nil error from recovered panic, got nil")
	}
	if result == nil {
		t.Fatalf("expected non-nil ToolResult from recovered panic, got nil")
	}
	if !result.IsError {
		t.Fatalf("expected IsError=true on recovered-panic result, got false")
	}
	if len(result.Content) == 0 {
		t.Fatalf("expected Content to contain a text block describing the panic")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "panicked") {
		t.Fatalf("expected result text to mention %q, got %q", "panicked", text)
	}
	if !strings.Contains(text, "boom") {
		t.Fatalf("expected result text to name the panicking tool, got %q", text)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected error to name the panicking tool, got %q", err.Error())
	}
}

// TestRegistryExecuteRecoversNonStringPanic verifies that panics with
// non-string values (e.g. runtime errors, integers, custom types) are also
// recovered and surfaced cleanly.
func TestRegistryExecuteRecoversNonStringPanic(t *testing.T) {
	r := NewRegistry()
	r.Register(&panickingTool{name: "numeric_boom", val: 42})

	result, err := r.Execute(context.Background(), "numeric_boom", json.RawMessage(`{}`))

	if err == nil || result == nil || !result.IsError {
		t.Fatalf("expected recovered-panic error/result; got err=%v result=%v", err, result)
	}
	if !strings.Contains(result.Content[0].Text, "42") {
		t.Fatalf("expected result text to include panic value 42, got %q", result.Content[0].Text)
	}
}

// TestRegistryExecuteNormalToolUnaffected verifies the recover block does
// not interfere with a tool that returns cleanly.
func TestRegistryExecuteNormalToolUnaffected(t *testing.T) {
	r := NewRegistry()
	r.Register(&okTool{name: "fine"})

	result, err := r.Execute(context.Background(), "fine", json.RawMessage(`{}`))

	if err != nil {
		t.Fatalf("expected nil error from clean tool, got %v", err)
	}
	if result == nil {
		t.Fatalf("expected non-nil result from clean tool")
	}
	if result.IsError {
		t.Fatalf("expected IsError=false on clean-tool result, got true")
	}
	if len(result.Content) == 0 || result.Content[0].Text != "ok" {
		t.Fatalf("expected clean tool to return \"ok\", got %+v", result.Content)
	}
}

// TestRegistryExecuteUnknownTool preserves the pre-existing behavior of
// returning an error for an unregistered tool name (no panic path involved).
func TestRegistryExecuteUnknownTool(t *testing.T) {
	r := NewRegistry()

	result, err := r.Execute(context.Background(), "does_not_exist", json.RawMessage(`{}`))

	if err == nil {
		t.Fatalf("expected error for unknown tool, got nil")
	}
	if result != nil {
		t.Fatalf("expected nil result for unknown tool, got %+v", result)
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected error to mention %q, got %q", "unknown tool", err.Error())
	}
}
