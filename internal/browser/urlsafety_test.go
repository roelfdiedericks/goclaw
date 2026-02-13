package browser

import (
	"net"
	"testing"
)

func TestValidateURLSafety(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string // substring to look for in error
	}{
		// Valid URLs
		{"valid https", "https://google.com", false, ""},
		{"valid http", "http://example.com", false, ""},
		{"valid with port", "https://example.com:8080/path", false, ""},
		{"valid with path", "https://example.com/path/to/page", false, ""},

		// Blocked schemes
		{"file scheme", "file:///etc/passwd", true, "scheme"},
		{"ftp scheme", "ftp://example.com", true, "scheme"},
		{"javascript scheme", "javascript:alert(1)", true, "scheme"},
		{"data scheme", "data:text/html,<h1>hi</h1>", true, "scheme"},
		{"gopher scheme", "gopher://localhost", true, "scheme"},

		// Localhost variants
		{"localhost", "http://localhost", true, "loopback"},
		{"localhost with port", "http://localhost:8080", true, "loopback"},
		{"127.0.0.1", "http://127.0.0.1", true, "loopback"},
		{"127.0.0.1 with port", "http://127.0.0.1:3000", true, "loopback"},
		{"127.1 short form", "http://127.1", true, ""},     // May fail DNS or resolve to loopback - either is safe
		{"127.0.1 short form", "http://127.0.1", true, ""}, // May fail DNS or resolve to loopback - either is safe
		{"127.x.x.x range", "http://127.255.255.255", true, "loopback"},

		// IPv6 loopback
		{"ipv6 loopback", "http://[::1]", true, "loopback"},
		{"ipv6 loopback full", "http://[0000::1]", true, "loopback"},

		// Private networks
		{"10.x.x.x", "http://10.0.0.1", true, "private"},
		{"172.16.x.x", "http://172.16.0.1", true, "private"},
		{"172.31.x.x", "http://172.31.255.255", true, "private"},
		{"192.168.x.x", "http://192.168.1.1", true, "private"},

		// Link-local (includes cloud metadata)
		{"link-local", "http://169.254.1.1", true, "link-local"},
		{"aws metadata", "http://169.254.169.254", true, "link-local"},
		{"aws metadata with path", "http://169.254.169.254/latest/meta-data/", true, "link-local"},

		// Cloud metadata hostnames
		{"gcp metadata", "http://metadata.google.internal", true, "cloud metadata hostname"},

		// Unspecified
		{"0.0.0.0", "http://0.0.0.0", true, "unspecified"},

		// Edge cases
		{"empty host", "http:///path", true, "empty hostname"},
		{"no scheme", "example.com", true, "scheme"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURLSafety(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURLSafety(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errMsg != "" {
				if err == nil || !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateURLSafety(%q) error = %v, want error containing %q", tt.url, err, tt.errMsg)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && searchString(s, substr)))
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestIsBlockedIP(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		blocked bool
	}{
		// Public IPs - should be allowed
		{"google dns", "8.8.8.8", false},
		{"cloudflare dns", "1.1.1.1", false},

		// Loopback
		{"loopback", "127.0.0.1", true},
		{"loopback range", "127.255.255.255", true},

		// Private
		{"private 10.x", "10.0.0.1", true},
		{"private 172.16.x", "172.16.0.1", true},
		{"private 192.168.x", "192.168.0.1", true},

		// Link-local
		{"link-local", "169.254.1.1", true},
		{"metadata", "169.254.169.254", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP: %s", tt.ip)
			}
			reason := isBlockedIP(ip)
			blocked := reason != ""
			if blocked != tt.blocked {
				t.Errorf("isBlockedIP(%s) = %q (blocked=%v), want blocked=%v", tt.ip, reason, blocked, tt.blocked)
			}
		})
	}
}
