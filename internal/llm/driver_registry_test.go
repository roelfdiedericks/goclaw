package llm

import "testing"

func TestSetupAPIKeyRequired(t *testing.T) {
	tests := []struct {
		name     string
		driver   string
		endpoint string
		want     bool
	}{
		{
			name:     "remote anthropic requires key",
			driver:   "anthropic",
			endpoint: "https://api.anthropic.com",
			want:     true,
		},
		{
			name:     "local endpoint does not require key during setup",
			driver:   "openai",
			endpoint: "http://127.0.0.1:1234/v1",
			want:     false,
		},
		{
			name:     "remote ollama does not require key during setup",
			driver:   "ollama",
			endpoint: "http://10.0.0.25:11434",
			want:     false,
		},
		{
			name:     "llamacpp driver does not require key during setup",
			driver:   "llamacpp",
			endpoint: "http://10.0.0.99:8080/v1",
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SetupAPIKeyRequired(tc.driver, tc.endpoint); got != tc.want {
				t.Fatalf("SetupAPIKeyRequired(%q, %q) = %v, want %v", tc.driver, tc.endpoint, got, tc.want)
			}
		})
	}
}
