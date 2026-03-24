package browser

import "testing"

func TestParseRemoteProfilesText(t *testing.T) {
	text := `
# comment
workstation=ws://192.168.1.50:9222/devtools/browser/abc123
staging=http://10.0.0.20:9222
invalid-line
empty=
`

	profiles := parseRemoteProfilesText(text)

	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}
	if profiles["workstation"].Endpoint != "ws://192.168.1.50:9222/devtools/browser/abc123" {
		t.Fatalf("unexpected workstation endpoint: %q", profiles["workstation"].Endpoint)
	}
	if profiles["staging"].Endpoint != "http://10.0.0.20:9222" {
		t.Fatalf("unexpected staging endpoint: %q", profiles["staging"].Endpoint)
	}
}

func TestToolsConfigAdapterToConfigRemoteFields(t *testing.T) {
	cfg := ToolsConfigAdapter{
		ChromeCDP:                    "ws://localhost:9333",
		AllowAgentProfiles:           true,
		RemoteEnabled:                true,
		RemoteProfilesText:           "workstation=ws://192.168.1.50:9222/devtools/browser/abc123",
		RemoteAllowedHosts:           []string{"192.168.1.50"},
		RemoteAllowDirectEndpoints:   true,
		RemoteAllowHTTPDiscovery:     true,
		RemoteConnectionTimeout:      "15s",
		AdvancedNetworkCaptureEnabled: true,
		AdvancedNetworkCaptureMax:     123,
		AdvancedConsoleCaptureEnabled: true,
		AdvancedConsoleCaptureMax:     77,
		AdvancedTraceDir:              "media/browser/traces",
		AdvancedTraceRetention:        9,
	}.ToConfig()

	if cfg.ChromeCDP != "ws://localhost:9333" {
		t.Fatalf("expected ChromeCDP to be copied, got %q", cfg.ChromeCDP)
	}
	if !cfg.AllowAgentProfiles {
		t.Fatalf("expected AllowAgentProfiles to be true")
	}
	if !cfg.Remote.Enabled {
		t.Fatalf("expected remote browser config to be enabled")
	}
	if cfg.Remote.ConnectionTimeout != "15s" {
		t.Fatalf("expected remote timeout to be copied, got %q", cfg.Remote.ConnectionTimeout)
	}
	if _, ok := cfg.ResolveRemoteProfile("workstation"); !ok {
		t.Fatalf("expected configured remote profile to be resolved")
	}
	if !cfg.Advanced.NetworkCaptureEnabled || cfg.Advanced.NetworkCaptureMax != 123 {
		t.Fatalf("expected advanced network capture settings to be copied")
	}
	if !cfg.Advanced.ConsoleCaptureEnabled || cfg.Advanced.ConsoleCaptureMax != 77 {
		t.Fatalf("expected advanced console capture settings to be copied")
	}
	if cfg.Advanced.TraceDir != "media/browser/traces" || cfg.Advanced.TraceRetention != 9 {
		t.Fatalf("expected advanced trace settings to be copied")
	}
}

func TestRemoteProfileHelpers(t *testing.T) {
	if !IsRemoteProfile("remote:workstation") {
		t.Fatalf("expected remote profile prefix to be detected")
	}
	if IsRemoteProfile("chrome") {
		t.Fatalf("did not expect chrome to be treated as remote profile")
	}
	if got := RemoteProfileName("remote:workstation"); got != "workstation" {
		t.Fatalf("expected remote profile name extraction, got %q", got)
	}
}

func TestResolveDeviceStrict(t *testing.T) {
	if _, ok := ResolveDeviceStrict("iphone-x"); !ok {
		t.Fatalf("expected iphone-x to be a known device profile")
	}
	if _, ok := ResolveDeviceStrict("definitely-not-a-device"); ok {
		t.Fatalf("did not expect unknown device profile to resolve successfully")
	}
}
