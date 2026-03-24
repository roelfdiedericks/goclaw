package browser

import "testing"

func TestAppendBoundedConsole(t *testing.T) {
	var msgs []ConsoleMessage
	msgs = appendBoundedConsole(msgs, ConsoleMessage{Text: "one"}, 2)
	msgs = appendBoundedConsole(msgs, ConsoleMessage{Text: "two"}, 2)
	msgs = appendBoundedConsole(msgs, ConsoleMessage{Text: "three"}, 2)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 console messages, got %d", len(msgs))
	}
	if msgs[0].Text != "two" || msgs[1].Text != "three" {
		t.Fatalf("unexpected bounded console contents: %#v", msgs)
	}
}

func TestUpsertNetworkRecordBounds(t *testing.T) {
	capture := &TabCapture{NetworkByID: map[string]*NetworkRecord{}}

	upsertNetworkRecord(capture, &NetworkRecord{ID: "1", URL: "https://a"}, 2)
	upsertNetworkRecord(capture, &NetworkRecord{ID: "2", URL: "https://b"}, 2)
	upsertNetworkRecord(capture, &NetworkRecord{ID: "3", URL: "https://c"}, 2)

	if len(capture.NetworkOrder) != 2 {
		t.Fatalf("expected bounded network order of 2, got %d", len(capture.NetworkOrder))
	}
	if capture.NetworkOrder[0] != "2" || capture.NetworkOrder[1] != "3" {
		t.Fatalf("unexpected network order: %#v", capture.NetworkOrder)
	}
	if _, ok := capture.NetworkByID["1"]; ok {
		t.Fatalf("expected oldest record to be evicted")
	}
}

func TestEnsureNetworkRecordReusesExisting(t *testing.T) {
	capture := &TabCapture{NetworkByID: map[string]*NetworkRecord{}}
	first := ensureNetworkRecord(capture, "abc", 10)
	second := ensureNetworkRecord(capture, "abc", 10)

	if first != second {
		t.Fatalf("expected ensureNetworkRecord to reuse existing record")
	}
	if len(capture.NetworkOrder) != 1 {
		t.Fatalf("expected one network record, got %d", len(capture.NetworkOrder))
	}
}

func TestNetworkPresetParams(t *testing.T) {
	tests := []struct {
		preset  string
		wantErr bool
		offline bool
	}{
		{preset: "clear", wantErr: false, offline: false},
		{preset: "offline", wantErr: false, offline: true},
		{preset: "fast-3g", wantErr: false, offline: false},
		{preset: "slow-3g", wantErr: false, offline: false},
		{preset: "bad", wantErr: true},
	}

	for _, tt := range tests {
		params, err := networkPresetParams(tt.preset)
		if (err != nil) != tt.wantErr {
			t.Fatalf("networkPresetParams(%q) error=%v wantErr=%v", tt.preset, err, tt.wantErr)
		}
		if err == nil && params.Offline != tt.offline {
			t.Fatalf("networkPresetParams(%q) offline=%v want=%v", tt.preset, params.Offline, tt.offline)
		}
	}
}

func TestSessionIDForProfile(t *testing.T) {
	if got := sessionIDForProfile("default", ""); got != "default" {
		t.Fatalf("expected empty profile to keep default session ID, got %q", got)
	}
	if got := sessionIDForProfile("default", "chrome"); got != "default::chrome" {
		t.Fatalf("expected explicit profile session suffix, got %q", got)
	}
	if got := sessionIDForProfile("default", "remote:workstation"); got != "default::remote:workstation" {
		t.Fatalf("expected remote profile session suffix, got %q", got)
	}
}
