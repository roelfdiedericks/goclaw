package infratui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/roelfdiedericks/goclaw/internal/a2a"
	"github.com/roelfdiedericks/goclaw/internal/logging"
)

const (
	refreshInterval = 2 * time.Second
	maxLogLines     = 400
)

var (
	focusedBorderColor   = tcell.ColorAqua
	unfocusedBorderColor = tcell.ColorWhite
)

type focusPane int

const (
	focusPeers focusPane = iota
	focusRendezvous
	focusLogs
)

type monitor struct {
	ctx     context.Context
	manager *a2a.Manager
	app     *tview.Application

	header     *tview.TextView
	summary    *tview.TextView
	peerList   *tview.List
	rvList     *tview.List
	detail     *tview.TextView
	logView    *tview.TextView
	footer     *tview.TextView
	lastUpdate *tview.TextView

	mu             sync.Mutex
	snapshot       a2a.InfraSnapshot
	lastFingerprint string
	logLines       []string
	selectedPeerID string
	selectedRVKey  string
	focus          focusPane
	suppressChange bool
}

func Run(ctx context.Context, manager *a2a.Manager, initialLogLines []string) error {
	m := newMonitor(ctx, manager)
	m.applySnapshot(manager.InfraSnapshot())
	for _, line := range initialLogLines {
		m.appendLog(line)
	}

	root := m.layout()
	m.app.SetRoot(root, true)
	m.app.SetFocus(m.peerList)

	logging.SetHookExclusive(func(level, msg string) {
		formatted := fmt.Sprintf("%s [%s] %s", time.Now().Format("15:04:05"), level, msg)
		m.app.QueueUpdateDraw(func() {
			m.appendLog(formatted)
		})
	})
	defer logging.SetHookExclusive(nil)

	go m.runRefreshLoop()
	go func() {
		<-ctx.Done()
		m.app.Stop()
	}()

	return m.app.EnableMouse(true).Run()
}

func newMonitor(ctx context.Context, manager *a2a.Manager) *monitor {
	m := &monitor{
		ctx:     ctx,
		manager: manager,
		app:     tview.NewApplication(),
		header:  tview.NewTextView().SetDynamicColors(true).SetWrap(true),
		summary: tview.NewTextView().SetDynamicColors(true).SetWrap(true),
		peerList: tview.NewList().
			ShowSecondaryText(true).
			SetHighlightFullLine(true),
		rvList: tview.NewList().
			ShowSecondaryText(true).
			SetHighlightFullLine(true),
		detail:     tview.NewTextView().SetDynamicColors(true).SetWrap(true),
		logView:    tview.NewTextView().SetDynamicColors(true).SetWrap(false).SetScrollable(true),
		footer:     tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignCenter),
		lastUpdate: tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight),
		focus:      focusPeers,
	}

	m.header.SetBorder(true).SetTitle(" A2A Infra ").SetTitleAlign(tview.AlignLeft)
	m.summary.SetBorder(true).SetTitle(" Summary ").SetTitleAlign(tview.AlignLeft)
	m.peerList.SetBorder(true).SetTitle(" Connected Peers ").SetTitleAlign(tview.AlignLeft)
	m.rvList.SetBorder(true).SetTitle(" Rendezvous Entries ").SetTitleAlign(tview.AlignLeft)
	m.detail.SetBorder(true).SetTitle(" Details ").SetTitleAlign(tview.AlignLeft)
	m.logView.SetBorder(true).SetTitle(" Logs ").SetTitleAlign(tview.AlignLeft)
	m.lastUpdate.SetBorder(true).SetTitle(" Refreshed ").SetTitleAlign(tview.AlignLeft)
	m.footer.SetText("[gray]Tab switch pane  arrows move  mouse click selects  q quit")
	m.applyFocusStylingLocked()

	m.peerList.SetChangedFunc(func(index int, _, _ string, _ rune) {
		if m.suppressChange {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if index >= 0 && index < len(m.snapshot.Peers) {
			m.selectedPeerID = m.snapshot.Peers[index].PeerID
			m.focus = focusPeers
			m.applyFocusStylingLocked()
			m.renderDetailLocked()
		}
	})
	m.peerList.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseLeftClick {
			m.mu.Lock()
			m.focus = focusPeers
			m.applyFocusStylingLocked()
			m.renderDetailLocked()
			m.mu.Unlock()
			m.app.SetFocus(m.peerList)
		}
		return action, event
	})
	m.rvList.SetChangedFunc(func(index int, _, _ string, _ rune) {
		if m.suppressChange {
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		entries := flattenRendezvous(m.snapshot.Rendezvous)
		if index >= 0 && index < len(entries) {
			m.selectedRVKey = rendezvousKey(entries[index])
			m.focus = focusRendezvous
			m.applyFocusStylingLocked()
			m.renderDetailLocked()
		}
	})
	m.rvList.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseLeftClick {
			m.mu.Lock()
			m.focus = focusRendezvous
			m.applyFocusStylingLocked()
			m.renderDetailLocked()
			m.mu.Unlock()
			m.app.SetFocus(m.rvList)
		}
		return action, event
	})
	m.logView.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseLeftClick {
			m.mu.Lock()
			m.focus = focusLogs
			m.applyFocusStylingLocked()
			m.renderDetailLocked()
			m.mu.Unlock()
			m.app.SetFocus(m.logView)
		}
		return action, event
	})

	inputCapture := func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC:
			m.app.Stop()
			return nil
		case tcell.KeyTab:
			m.advanceFocus()
			return nil
		}
		switch event.Rune() {
		case 'q', 'Q':
			m.app.Stop()
			return nil
		}
		return event
	}
	m.peerList.SetInputCapture(inputCapture)
	m.rvList.SetInputCapture(inputCapture)
	m.logView.SetInputCapture(inputCapture)

	return m
}

func (m *monitor) layout() tview.Primitive {
	top := tview.NewFlex().
		AddItem(m.header, 0, 3, false).
		AddItem(m.lastUpdate, 24, 0, false)

	middle := tview.NewFlex().
		AddItem(m.peerList, 0, 1, true).
		AddItem(m.rvList, 0, 1, false)

	return tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(top, 3, 0, false).
		AddItem(m.summary, 5, 0, false).
		AddItem(middle, 0, 2, true).
		AddItem(m.detail, 10, 0, false).
		AddItem(m.logView, 0, 2, false).
		AddItem(m.footer, 1, 0, false)
}

func (m *monitor) runRefreshLoop() {
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			snapshot := m.manager.InfraSnapshot()
			fingerprint := snapshot.Fingerprint()
			m.app.QueueUpdateDraw(func() {
				if fingerprint == m.lastFingerprint {
					m.lastUpdate.SetText(time.Now().Format("15:04:05"))
					return
				}
				m.applySnapshot(snapshot)
			})
		}
	}
}

func (m *monitor) applySnapshot(snapshot a2a.InfraSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.snapshot = snapshot
	m.lastFingerprint = snapshot.Fingerprint()
	m.renderHeaderLocked()
	m.renderSummaryLocked()
	m.rebuildPeerListLocked()
	m.rebuildRendezvousListLocked()
	m.renderDetailLocked()
	m.lastUpdate.SetText(snapshot.CapturedAt.Format("15:04:05"))
}

func (m *monitor) appendLog(line string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.logLines = append(m.logLines, line)
	if len(m.logLines) > maxLogLines {
		m.logLines = append([]string(nil), m.logLines[len(m.logLines)-maxLogLines:]...)
	}
	m.logView.SetText(strings.Join(m.logLines, "\n"))
	m.logView.ScrollToEnd()
}

func (m *monitor) renderHeaderLocked() {
	status := m.snapshot.Status
	header := fmt.Sprintf(
		"[white]Mode:[-] %s  [white]Lifecycle:[-] %s  [white]Ready:[-] %t  [white]PeerID:[-] %s",
		status.RuntimeMode,
		status.LifecycleState,
		status.Ready,
		emptyDash(status.LocalPeerID),
	)
	m.header.SetText(header)
}

func (m *monitor) renderSummaryLocked() {
	summary := m.snapshot.Summary
	status := m.snapshot.Status
	lines := []string{
		fmt.Sprintf("[white]Connected:[-] %d  [white]Direct:[-] %d  [white]Relayed:[-] %d", summary.ConnectedPeers, summary.ConnectedDirectPeers, summary.ConnectedRelayedPeers),
		fmt.Sprintf("[white]Connected by state:[-] %s", emptyDash(formatCounts(summary.ConnectedPeerStateCount))),
		fmt.Sprintf("[white]Rendezvous entries:[-] %d  [white]Namespaces:[-] %d", summary.RendezvousEntries, summary.RendezvousNamespaces),
		fmt.Sprintf("[white]Rendezvous by namespace:[-] %s", emptyDash(formatCounts(summary.RendezvousByNamespace))),
		fmt.Sprintf("[white]Reachability:[-] %s  [white]Listen:[-] %d  [white]Advertised:[-] %d  [white]Relay addrs:[-] %d", emptyDash(status.Reachability), len(status.ListenAddrs), len(status.AdvertisedAddrs), len(status.RelayAddrs)),
		fmt.Sprintf("[white]Port map:[-] %t  [white]Auto relay:[-] %t  [white]Hole punch:[-] %t", status.NATPortMapEnabled, status.AutoRelayEnabled, status.HolePunchEnabled),
	}
	m.summary.SetText(strings.Join(lines, "\n"))
}

func (m *monitor) rebuildPeerListLocked() {
	m.suppressChange = true
	defer func() { m.suppressChange = false }()

	m.peerList.Clear()
	for _, peer := range m.snapshot.Peers {
		mainText := fmt.Sprintf("%s [%s]", peer.PeerID, peer.State)
		if strings.TrimSpace(peer.Alias) != "" {
			mainText = fmt.Sprintf("%s (%s) [%s]", peer.PeerID, peer.Alias, peer.State)
		}
		secondary := fmt.Sprintf("relayed=%t addrs=%d", peer.Relayed, len(peer.Addrs))
		peerID := peer.PeerID
		m.peerList.AddItem(mainText, secondary, 0, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.selectedPeerID = peerID
			m.focus = focusPeers
			m.renderDetailLocked()
		})
	}
	if len(m.snapshot.Peers) == 0 {
		m.selectedPeerID = ""
		return
	}
	index := 0
	if strings.TrimSpace(m.selectedPeerID) == "" {
		m.selectedPeerID = m.snapshot.Peers[0].PeerID
	} else {
		for i, peer := range m.snapshot.Peers {
			if peer.PeerID == m.selectedPeerID {
				index = i
				break
			}
		}
	}
	m.peerList.SetCurrentItem(index)
}

func (m *monitor) rebuildRendezvousListLocked() {
	m.suppressChange = true
	defer func() { m.suppressChange = false }()

	m.rvList.Clear()
	entries := flattenRendezvous(m.snapshot.Rendezvous)
	for _, entry := range entries {
		mainText := fmt.Sprintf("%s (%s)", entry.PeerID, entry.Namespace)
		secondary := fmt.Sprintf("expires=%s addrs=%d", entry.ExpiresAt.Format("15:04:05"), len(entry.Addrs))
		key := rendezvousKey(entry)
		m.rvList.AddItem(mainText, secondary, 0, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.selectedRVKey = key
			m.focus = focusRendezvous
			m.renderDetailLocked()
		})
	}
	if len(entries) == 0 {
		m.selectedRVKey = ""
		return
	}
	index := 0
	if strings.TrimSpace(m.selectedRVKey) == "" {
		m.selectedRVKey = rendezvousKey(entries[0])
	} else {
		for i, entry := range entries {
			if rendezvousKey(entry) == m.selectedRVKey {
				index = i
				break
			}
		}
	}
	m.rvList.SetCurrentItem(index)
}

func (m *monitor) renderDetailLocked() {
	switch m.focus {
	case focusRendezvous:
		if entry, ok := m.selectedRendezvousEntryLocked(); ok {
			lines := []string{
				"[white]Selected:[-] rendezvous entry",
				fmt.Sprintf("[white]Namespace:[-] %s", entry.Namespace),
				fmt.Sprintf("[white]PeerID:[-] %s", entry.PeerID),
				fmt.Sprintf("[white]Expires:[-] %s", entry.ExpiresAt.Format(time.RFC3339)),
				"[white]Registered addrs:[-]",
			}
			if len(entry.Addrs) == 0 {
				lines = append(lines, "  -")
			} else {
				for _, addr := range entry.Addrs {
					lines = append(lines, "  "+addr)
				}
			}
			m.detail.SetText(strings.Join(lines, "\n"))
			return
		}
	case focusPeers:
		if peer, ok := m.selectedPeerLocked(); ok {
			lines := []string{
				"[white]Selected:[-] connected peer",
				fmt.Sprintf("[white]PeerID:[-] %s", peer.PeerID),
				fmt.Sprintf("[white]Alias:[-] %s", emptyDash(peer.Alias)),
				fmt.Sprintf("[white]State:[-] %s", peer.State),
				fmt.Sprintf("[white]Relayed:[-] %t", peer.Relayed),
				fmt.Sprintf("[white]Authorized:[-] %t", peer.Authorized),
				fmt.Sprintf("[white]Last connected:[-] %s", formatTime(peer.LastConnectedAt)),
				"[white]Remote addrs:[-]",
			}
			if len(peer.Addrs) == 0 {
				lines = append(lines, "  -")
			} else {
				for _, addr := range peer.Addrs {
					lines = append(lines, "  "+addr)
				}
			}
			m.detail.SetText(strings.Join(lines, "\n"))
			return
		}
	}

	status := m.snapshot.Status
	lines := []string{
		"[white]Selected:[-] local infra node",
		fmt.Sprintf("[white]Mode:[-] %s", status.RuntimeMode),
		fmt.Sprintf("[white]Lifecycle:[-] %s", status.LifecycleState),
		fmt.Sprintf("[white]Ready:[-] %t", status.Ready),
		fmt.Sprintf("[white]PeerID:[-] %s", emptyDash(status.LocalPeerID)),
		fmt.Sprintf("[white]Reachability:[-] %s", emptyDash(status.Reachability)),
		fmt.Sprintf("[white]Port map:[-] %t", status.NATPortMapEnabled),
		fmt.Sprintf("[white]Auto relay:[-] %t", status.AutoRelayEnabled),
		fmt.Sprintf("[white]Hole punch:[-] %t", status.HolePunchEnabled),
		fmt.Sprintf("[white]Announce private addrs:[-] %t", status.AnnouncePrivate),
		"[white]Listen addrs:[-]",
	}
	if len(status.ListenAddrs) == 0 {
		lines = append(lines, "  -")
	} else {
		for _, addr := range status.ListenAddrs {
			lines = append(lines, "  "+addr)
		}
	}
	lines = append(lines, "[white]Advertised addrs:[-]")
	if len(status.AdvertisedAddrs) == 0 {
		lines = append(lines, "  -")
	} else {
		for _, addr := range status.AdvertisedAddrs {
			lines = append(lines, "  "+addr)
		}
	}
	lines = append(lines, "[white]Relay addrs:[-]")
	if len(status.RelayAddrs) == 0 {
		lines = append(lines, "  -")
	} else {
		for _, addr := range status.RelayAddrs {
			lines = append(lines, "  "+addr)
		}
	}
	m.detail.SetText(strings.Join(lines, "\n"))
}

func (m *monitor) selectedPeerLocked() (a2a.InfraConnectedPeer, bool) {
	for _, peer := range m.snapshot.Peers {
		if peer.PeerID == m.selectedPeerID {
			return peer, true
		}
	}
	return a2a.InfraConnectedPeer{}, false
}

func (m *monitor) selectedRendezvousEntryLocked() (a2a.InfraRendezvousEntry, bool) {
	for _, namespace := range m.snapshot.Rendezvous {
		for _, entry := range namespace.Entries {
			if rendezvousKey(entry) == m.selectedRVKey {
				return entry, true
			}
		}
	}
	return a2a.InfraRendezvousEntry{}, false
}

func (m *monitor) advanceFocus() {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch m.focus {
	case focusPeers:
		m.focus = focusRendezvous
		m.applyFocusStylingLocked()
		m.app.SetFocus(m.rvList)
	case focusRendezvous:
		m.focus = focusLogs
		m.applyFocusStylingLocked()
		m.app.SetFocus(m.logView)
	default:
		m.focus = focusPeers
		m.applyFocusStylingLocked()
		m.app.SetFocus(m.peerList)
	}
	m.renderDetailLocked()
}

func (m *monitor) applyFocusStylingLocked() {
	m.peerList.SetBorderColor(unfocusedBorderColor)
	m.rvList.SetBorderColor(unfocusedBorderColor)
	m.logView.SetBorderColor(unfocusedBorderColor)

	switch m.focus {
	case focusPeers:
		m.peerList.SetBorderColor(focusedBorderColor)
	case focusRendezvous:
		m.rvList.SetBorderColor(focusedBorderColor)
	case focusLogs:
		m.logView.SetBorderColor(focusedBorderColor)
	}
}

func flattenRendezvous(namespaces []a2a.InfraRendezvousNamespace) []a2a.InfraRendezvousEntry {
	out := make([]a2a.InfraRendezvousEntry, 0)
	for _, namespace := range namespaces {
		out = append(out, namespace.Entries...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace == out[j].Namespace {
			return out[i].PeerID < out[j].PeerID
		}
		return out[i].Namespace < out[j].Namespace
	})
	return out
}

func rendezvousKey(entry a2a.InfraRendezvousEntry) string {
	return entry.Namespace + ":" + entry.PeerID
}

func formatCounts(values map[string]int) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, values[key]))
	}
	return strings.Join(parts, ", ")
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Format(time.RFC3339)
}
