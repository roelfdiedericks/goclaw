package browser

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateRemoteHost(t *testing.T) {
	mgr := &Manager{
		config: BrowserConfig{
			Remote: RemoteBrowserConfig{
				AllowedHosts: []string{"192.168.1.50", "10.0.0.0/24", "*.example.com"},
			},
		},
	}

	tests := []struct {
		host    string
		wantErr bool
	}{
		{host: "192.168.1.50", wantErr: false},
		{host: "10.0.0.25", wantErr: false},
		{host: "api.example.com", wantErr: false},
		{host: "192.168.1.99", wantErr: true},
		{host: "example.net", wantErr: true},
	}

	for _, tt := range tests {
		err := mgr.validateRemoteHost(tt.host)
		if (err != nil) != tt.wantErr {
			t.Fatalf("validateRemoteHost(%q) error=%v wantErr=%v", tt.host, err, tt.wantErr)
		}
	}
}

func TestResolveControlURLViaHTTPDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("expected /json/version, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/abc123"}`))
	}))
	defer server.Close()

	mgr := &Manager{
		config: BrowserConfig{
			Remote: RemoteBrowserConfig{
				AllowHTTPDiscovery: true,
			},
		},
	}

	resolved, err := mgr.resolveControlURL(server.URL, false)
	if err != nil {
		t.Fatalf("resolveControlURL returned error: %v", err)
	}
	if resolved != "ws://127.0.0.1:9222/devtools/browser/abc123" {
		t.Fatalf("unexpected resolved websocket URL: %q", resolved)
	}
}

func TestIsExternalProfileIncludesRemoteProfiles(t *testing.T) {
	mgr := &Manager{}
	if !mgr.IsExternalProfile("chrome") {
		t.Fatalf("expected chrome to be external")
	}
	if !mgr.IsExternalProfile("remote:workstation") {
		t.Fatalf("expected remote profile to be external")
	}
	if mgr.IsExternalProfile("default") {
		t.Fatalf("did not expect managed local profile to be external")
	}
}
