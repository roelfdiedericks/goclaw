package delegatedrun

import "testing"

func TestMemoryRegistryClaimCompletionDispatchExclusivePerSeq(t *testing.T) {
	reg := NewMemoryRegistry()
	err := reg.Create(RunRecord{
		RunID:                 "run-1",
		CompletionDispatchSeq: 1,
		State:                 RunStateCompleted,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	claimed, err := reg.ClaimCompletionDispatch("run-1", "claim-a", 1)
	if err != nil || !claimed {
		t.Fatalf("expected first claim success, got claimed=%v err=%v", claimed, err)
	}
	claimed, err = reg.ClaimCompletionDispatch("run-1", "claim-b", 1)
	if err != nil {
		t.Fatalf("unexpected second claim error: %v", err)
	}
	if claimed {
		t.Fatalf("expected second claim for same seq to be rejected")
	}
	if err := reg.ReleaseCompletionDispatch("run-1", "claim-a"); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	claimed, err = reg.ClaimCompletionDispatch("run-1", "claim-c", 2)
	if err != nil || !claimed {
		t.Fatalf("expected new seq claim success, got claimed=%v err=%v", claimed, err)
	}
}
