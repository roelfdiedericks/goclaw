package gateway

import "testing"

func TestNormalizeSuppressionToken(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantToken string
		wantMatch bool
	}{
		{name: "canonical", input: "SILENT_OK", wantToken: canonicalSilentToken, wantMatch: true},
		{name: "canonical trimmed lowercase", input: "  silent_ok  ", wantToken: canonicalSilentToken, wantMatch: true},
		{name: "heartbeat alias", input: "HEARTBEAT_OK", wantToken: canonicalSilentToken, wantMatch: true},
		{name: "event alias", input: "event_ok", wantToken: canonicalSilentToken, wantMatch: true},
		{name: "no reply alias", input: "NO_REPLY", wantToken: canonicalSilentToken, wantMatch: true},
		{name: "non-match", input: "nothing", wantToken: "", wantMatch: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizeSuppressionToken(tc.input)
			if ok != tc.wantMatch {
				t.Fatalf("normalizeSuppressionToken(%q) match = %v, want %v", tc.input, ok, tc.wantMatch)
			}
			if got != tc.wantToken {
				t.Fatalf("normalizeSuppressionToken(%q) token = %q, want %q", tc.input, got, tc.wantToken)
			}
		})
	}
}

func TestShouldSuppressResponseUsesExactMatch(t *testing.T) {
	if !shouldSuppressResponse("SILENT_OK") {
		t.Fatalf("expected exact canonical token to suppress")
	}
	if !shouldSuppressResponse("  EVENT_OK  ") {
		t.Fatalf("expected exact alias token to suppress")
	}
	if shouldSuppressResponse("here is some text SILENT_OK") {
		t.Fatalf("expected token embedded in prose to not suppress")
	}
	if shouldSuppressResponse("SILENT_OK and more") {
		t.Fatalf("expected token with extra text to not suppress")
	}
}
