package llm

import "testing"

func TestLlamaSlotManagerAcquireReuseAndExhaustion(t *testing.T) {
	m := &llamaSlotManager{
		capByServer: make(map[string]int),
		slotOwner:   make(map[string]map[int]string),
		ownerSlot:   make(map[string]int),
	}
	const root = "http://127.0.0.1:1"
	m.SyncCapacity(root, 2)

	id, pinned := m.Acquire(root, "sess|p|m", 0)
	if !pinned || id != 0 {
		t.Fatalf("expected preferred slot 0, got %d pinned=%v", id, pinned)
	}
	id2, pinned2 := m.Acquire(root, "sess|p|m", 0)
	if !pinned2 || id2 != 0 {
		t.Fatalf("expected same lease 0, got %d pinned=%v", id2, pinned2)
	}

	idB, pinnedB := m.Acquire(root, "sess2|p|m", 1)
	if !pinnedB || idB != 1 {
		t.Fatalf("expected slot 1 for other owner, got %d pinned=%v", idB, pinnedB)
	}

	_, pinnedC := m.Acquire(root, "sess3|p|m", -1)
	if pinnedC {
		t.Fatal("expected exhaustion (no free slot)")
	}

	m.Release(root, "sess|p|m")
	idD, pinnedD := m.Acquire(root, "sess3|p|m", -1)
	if !pinnedD || idD != 0 {
		t.Fatalf("after release expected slot 0, got %d pinned=%v", idD, pinnedD)
	}
}
