package acp

import "testing"

func TestNewCursorDriverUsesDefaultModel(t *testing.T) {
	driver := NewCursorDriver()
	spec, err := driver.LaunchSpec(t.Context(), LaunchSpecRequest{})
	if err != nil {
		t.Fatalf("LaunchSpec returned error: %v", err)
	}
	if len(spec.Args) != 1 {
		t.Fatalf("expected 1 launch arg, got %d (%v)", len(spec.Args), spec.Args)
	}
	if spec.Args[0] != "acp" {
		t.Fatalf("unexpected launch args: %v", spec.Args)
	}
}
