package browser

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// URLSafetyError represents a URL that was blocked for safety reasons
type URLSafetyError struct {
	URL    string
	Reason string
}

func (e *URLSafetyError) Error() string {
	return fmt.Sprintf("URL blocked: %s", e.Reason)
}

// ValidateURLSafety checks if a URL is safe to navigate to.
// This protects against SSRF attacks by:
// - Allowing only http/https schemes
// - Resolving hostname to IP to catch encoding tricks
// - Blocking loopback, private, link-local, and cloud metadata IPs
//
// This check is performed regardless of bubblewrap status because
// bubblewrap sandboxes the filesystem, not the network.
func ValidateURLSafety(urlStr string) error {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return &URLSafetyError{URL: urlStr, Reason: fmt.Sprintf("invalid URL: %v", err)}
	}

	// Scheme check - only http/https allowed
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return &URLSafetyError{URL: urlStr, Reason: fmt.Sprintf("scheme '%s' not allowed, only http/https", parsed.Scheme)}
	}

	host := parsed.Hostname()
	if host == "" {
		return &URLSafetyError{URL: urlStr, Reason: "empty hostname"}
	}

	// Check for cloud metadata hostnames before DNS resolution
	if isCloudMetadataHost(host) {
		return &URLSafetyError{URL: urlStr, Reason: fmt.Sprintf("cloud metadata hostname blocked: %s", host)}
	}

	// Resolve hostname to IP(s) - this catches:
	// - Decimal IP encoding (2130706433 = 127.0.0.1)
	// - Hex IP encoding (0x7f000001 = 127.0.0.1)
	// - Octal IP encoding (0177.0.0.1 = 127.0.0.1)
	// - Domain redirects (localtest.me -> 127.0.0.1)
	// - Short forms (127.1 -> 127.0.0.1)
	ips, err := net.LookupIP(host)
	if err != nil {
		// If DNS fails, try parsing as IP directly
		// This handles cases like "127.0.0.1" which don't need DNS
		ip := net.ParseIP(host)
		if ip == nil {
			return &URLSafetyError{URL: urlStr, Reason: fmt.Sprintf("DNS resolution failed: %v", err)}
		}
		ips = []net.IP{ip}
	}

	// Check all resolved IPs
	for _, ip := range ips {
		if reason := isBlockedIP(ip); reason != "" {
			L_debug("urlsafety: blocked IP", "url", urlStr, "host", host, "ip", ip.String(), "reason", reason)
			return &URLSafetyError{URL: urlStr, Reason: fmt.Sprintf("%s (%s resolves to %s)", reason, host, ip.String())}
		}
	}

	L_trace("urlsafety: URL passed validation", "url", urlStr, "host", host, "ips", fmt.Sprintf("%v", ips))
	return nil
}

// isBlockedIP returns a reason string if the IP should be blocked, empty string if OK
func isBlockedIP(ip net.IP) string {
	// Loopback (127.0.0.0/8, ::1)
	if ip.IsLoopback() {
		return "loopback address blocked"
	}

	// Private ranges (10.x, 172.16-31.x, 192.168.x, fc00::/7)
	if ip.IsPrivate() {
		return "private network address blocked"
	}

	// Link-local (169.254.x.x, fe80::)
	if ip.IsLinkLocalUnicast() {
		return "link-local address blocked"
	}

	// Multicast
	if ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return "multicast address blocked"
	}

	// Unspecified (0.0.0.0, ::)
	if ip.IsUnspecified() {
		return "unspecified address blocked"
	}

	// Cloud metadata IP (169.254.169.254) - check explicitly since IsLinkLocalUnicast should catch it
	// but being explicit is safer
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return "cloud metadata address blocked"
	}

	// IPv4-mapped IPv6 addresses - unwrap and check the IPv4
	if ip4 := ip.To4(); ip4 != nil && !ip.Equal(ip4) {
		// This was an IPv6 address that maps to IPv4
		if reason := isBlockedIP(ip4); reason != "" {
			return reason + " (IPv4-mapped)"
		}
	}

	return ""
}

// isCloudMetadataHost checks for known cloud metadata hostnames
func isCloudMetadataHost(host string) bool {
	host = strings.ToLower(host)

	metadataHosts := []string{
		"metadata.google.internal", // GCP
		"metadata.goog",            // GCP alternate
		"kubernetes.default.svc",   // Kubernetes
		"kubernetes.default",       // Kubernetes
		"metadata",                 // Generic
	}

	for _, mh := range metadataHosts {
		if host == mh || strings.HasSuffix(host, "."+mh) {
			return true
		}
	}

	return false
}

// MustValidateURLSafety is like ValidateURLSafety but panics on error.
// Use only in tests.
func MustValidateURLSafety(urlStr string) {
	if err := ValidateURLSafety(urlStr); err != nil {
		panic(err)
	}
}
