package llm

import (
	"fmt"
	"net/netip"
	neturl "net/url"
	"slices"
	"strings"
	"sync"
)

// ProviderConstructor creates a provider implementation for a driver.
type ProviderConstructor func(name string, cfg LLMProviderConfig) (Provider, error)

// DriverDescriptor describes a supported provider driver.
type DriverDescriptor struct {
	ID                 string
	Label              string
	Order              int
	IsLocal            bool
	SupportsEmbeddings bool
	New                ProviderConstructor
}

var (
	driversMu sync.RWMutex
	drivers   = map[string]DriverDescriptor{}
)

// RegisterDriver registers a built-in provider driver.
// Intended for init()-time registration in each driver implementation file.
func RegisterDriver(desc DriverDescriptor) {
	if desc.ID == "" {
		panic("llm: RegisterDriver requires non-empty ID")
	}
	if desc.New == nil {
		panic(fmt.Sprintf("llm: RegisterDriver(%s) requires constructor", desc.ID))
	}

	driversMu.Lock()
	defer driversMu.Unlock()

	if _, exists := drivers[desc.ID]; exists {
		panic(fmt.Sprintf("llm: duplicate driver registration: %s", desc.ID))
	}
	drivers[desc.ID] = desc
}

// GetDriver returns a registered driver descriptor by ID.
func GetDriver(id string) (DriverDescriptor, bool) {
	driversMu.RLock()
	defer driversMu.RUnlock()
	desc, ok := drivers[id]
	return desc, ok
}

// ListDrivers returns all registered driver descriptors in deterministic order.
func ListDrivers() []DriverDescriptor {
	driversMu.RLock()
	defer driversMu.RUnlock()

	out := make([]DriverDescriptor, 0, len(drivers))
	for _, desc := range drivers {
		out = append(out, desc)
	}

	slices.SortFunc(out, func(a, b DriverDescriptor) int {
		if a.Order != b.Order {
			if a.Order < b.Order {
				return -1
			}
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out
}

// DriverIsLocal reports whether a driver is considered local-first.
func DriverIsLocal(driver string) bool {
	desc, ok := GetDriver(driver)
	return ok && desc.IsLocal
}

// EndpointIsLocal reports whether an endpoint points at a local/private host.
func EndpointIsLocal(endpoint string) bool {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return false
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := neturl.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// DriverOrEndpointIsLocal reports whether a provider should be treated as local.
func DriverOrEndpointIsLocal(driver, endpoint string) bool {
	return DriverIsLocal(driver) || EndpointIsLocal(endpoint)
}

