package cron

import "testing"

func TestParseResultMode(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{input: "store_only"},
		{input: "deliver"},
		{input: "handoff_main"},
		{input: "message", wantErr: true},
		{input: "", wantErr: true},
	}

	for _, tc := range tests {
		_, err := parseResultMode(tc.input)
		if tc.wantErr && err == nil {
			t.Fatalf("parseResultMode(%q) expected error", tc.input)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("parseResultMode(%q) unexpected error: %v", tc.input, err)
		}
	}
}
