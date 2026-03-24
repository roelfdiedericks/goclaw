package delegatedrun

import "testing"

func TestNormalizeStatusActionDefaults(t *testing.T) {
	action, err := NormalizeStatusAction("", "")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if action != "list" {
		t.Fatalf("expected list action, got %s", action)
	}

	action, err = NormalizeStatusAction("", "run-1")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if action != "info" {
		t.Fatalf("expected info action, got %s", action)
	}
}

func TestNormalizeCancelControlCascadeCompat(t *testing.T) {
	control, err := NormalizeCancelControl(CancelControlInput{
		Mode:    "cancel",
		Scope:   "self",
		Cascade: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if control.Scope != "subtree" || !control.Cascade {
		t.Fatalf("expected subtree cascade=true, got scope=%s cascade=%t", control.Scope, control.Cascade)
	}
}

func TestValidateCancelControlForRunRejectsKillSubtree(t *testing.T) {
	rec := RunRecord{
		State:         RunStateRunning,
		RequesterType: "subagent",
	}
	err := ValidateCancelControlForRun(rec, CancelControlPolicy{
		Mode:    "kill",
		Scope:   "subtree",
		Cascade: true,
	})
	if err == nil {
		t.Fatalf("expected error for kill subtree, got nil")
	}
}

func boolPtr(v bool) *bool { return &v }
