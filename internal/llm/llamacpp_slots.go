package llm

import (
	"strings"
	"sync"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// llamaSlotManager tracks client-assigned llama-server slot leases per server root.
// Leases are process-local; see prompt caching plan for unpinned fallback when full.
type llamaSlotManager struct {
	mu sync.Mutex

	capByServer map[string]int
	// serverRoot -> slotID -> lease owner key (for that slot)
	slotOwner map[string]map[int]string
	// serverRoot\x00ownerKey -> leased slot id
	ownerSlot map[string]int
}

var globalLlamaSlotManager = &llamaSlotManager{
	capByServer: make(map[string]int),
	slotOwner:   make(map[string]map[int]string),
	ownerSlot:   make(map[string]int),
}

func llamaSlotOwnerIndex(serverRoot, ownerKey string) string {
	var b strings.Builder
	b.Grow(len(serverRoot) + 1 + len(ownerKey))
	b.WriteString(serverRoot)
	b.WriteByte(0)
	b.WriteString(ownerKey)
	return b.String()
}

// SyncCapacity records total parallel slots reported by GET /props (>=1).
func (m *llamaSlotManager) SyncCapacity(serverRoot string, totalSlots int) {
	if serverRoot == "" {
		return
	}
	if totalSlots < 1 {
		totalSlots = 1
	}
	m.mu.Lock()
	m.capByServer[serverRoot] = totalSlots
	m.mu.Unlock()
}

// Acquire returns a pinned slot for ownerKey, preferring preferredSlot when free.
// If no slot is available, returns (0, false) and the caller should send an unpinned request.
func (m *llamaSlotManager) Acquire(serverRoot, ownerKey string, preferredSlot int) (slotID int, pinned bool) {
	if serverRoot == "" || ownerKey == "" {
		return 0, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	capacity := m.capByServer[serverRoot]
	if capacity < 1 {
		return 0, false
	}

	idx := llamaSlotOwnerIndex(serverRoot, ownerKey)
	if existing, ok := m.ownerSlot[idx]; ok {
		if existing >= 0 && existing < capacity {
			return existing, true
		}
		m.releaseLocked(serverRoot, ownerKey, existing)
	}

	tryAssign := func(slot int) bool {
		if slot < 0 || slot >= capacity {
			return false
		}
		bySlot := m.slotOwner[serverRoot]
		if bySlot != nil {
			if occ, taken := bySlot[slot]; taken && occ != ownerKey {
				return false
			}
		} else {
			bySlot = make(map[int]string)
			m.slotOwner[serverRoot] = bySlot
		}
		bySlot[slot] = ownerKey
		m.ownerSlot[idx] = slot
		L_trace("llamacpp slots: acquired",
			"serverRoot", serverRoot,
			"ownerKey", ownerKey,
			"slot", slot,
		)
		return true
	}

	if preferredSlot >= 0 && tryAssign(preferredSlot) {
		return preferredSlot, true
	}
	for i := 0; i < capacity; i++ {
		if tryAssign(i) {
			return i, true
		}
	}
	return 0, false
}

// Release drops the lease held by ownerKey on serverRoot, if any.
func (m *llamaSlotManager) Release(serverRoot, ownerKey string) {
	if serverRoot == "" || ownerKey == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := llamaSlotOwnerIndex(serverRoot, ownerKey)
	slot, ok := m.ownerSlot[idx]
	if !ok {
		return
	}
	m.releaseLocked(serverRoot, ownerKey, slot)
}

func (m *llamaSlotManager) releaseLocked(serverRoot, ownerKey string, slot int) {
	idx := llamaSlotOwnerIndex(serverRoot, ownerKey)
	delete(m.ownerSlot, idx)
	if bySlot := m.slotOwner[serverRoot]; bySlot != nil {
		if bySlot[slot] == ownerKey {
			delete(bySlot, slot)
		}
	}
	L_trace("llamacpp slots: released", "serverRoot", serverRoot, "ownerKey", ownerKey, "slot", slot)
}
