package memorygraph

import (
	"sync"
	"time"
)

// CachedBulletin holds cached bulletin content for a user with separate expiry times
type CachedBulletin struct {
	Memory           string
	MemoryExpiresAt  time.Time
	Context          string
	ContextExpiresAt time.Time
}

// BulletinCache provides per-user caching of generated bulletins
type BulletinCache struct {
	mu    sync.RWMutex
	items map[string]*CachedBulletin // keyed by username
	ttl   time.Duration
}

// NewBulletinCache creates a new bulletin cache with the specified TTL
func NewBulletinCache(ttl time.Duration) *BulletinCache {
	if ttl <= 0 {
		ttl = 5 * time.Minute // default 5 minutes
	}
	return &BulletinCache{
		items: make(map[string]*CachedBulletin),
		ttl:   ttl,
	}
}

// Get retrieves cached bulletins for a user, returning only non-expired values
func (c *BulletinCache) Get(username string) (memory, context string, memoryValid, contextValid bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cached, ok := c.items[username]
	if !ok {
		return "", "", false, false
	}

	now := time.Now()
	if !now.After(cached.MemoryExpiresAt) {
		memory = cached.Memory
		memoryValid = true
	}
	if !now.After(cached.ContextExpiresAt) {
		context = cached.Context
		contextValid = true
	}

	return memory, context, memoryValid, contextValid
}

// SetMemory stores the memory bulletin for a user
func (c *BulletinCache) SetMemory(username, memory string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cached, ok := c.items[username]
	if !ok {
		cached = &CachedBulletin{}
		c.items[username] = cached
	}
	cached.Memory = memory
	cached.MemoryExpiresAt = time.Now().Add(c.ttl)
}

// SetContext stores the context bulletin for a user
func (c *BulletinCache) SetContext(username, context string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cached, ok := c.items[username]
	if !ok {
		cached = &CachedBulletin{}
		c.items[username] = cached
	}
	cached.Context = context
	cached.ContextExpiresAt = time.Now().Add(c.ttl)
}

// Set stores both bulletins for a user with the configured TTL
func (c *BulletinCache) Set(username, memory, context string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	expiry := time.Now().Add(c.ttl)
	c.items[username] = &CachedBulletin{
		Memory:           memory,
		MemoryExpiresAt:  expiry,
		Context:          context,
		ContextExpiresAt: expiry,
	}
}

// InvalidateMemory invalidates only the memory bulletin for a user
func (c *BulletinCache) InvalidateMemory(username string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.items[username]; ok {
		cached.Memory = ""
		cached.MemoryExpiresAt = time.Time{} // zero time = expired
	}
}

// InvalidateContext invalidates only the context bulletin for a user
func (c *BulletinCache) InvalidateContext(username string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.items[username]; ok {
		cached.Context = ""
		cached.ContextExpiresAt = time.Time{} // zero time = expired
	}
}

// Invalidate removes all cached bulletins for a user
func (c *BulletinCache) Invalidate(username string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.items, username)
}

// InvalidateAll clears the entire cache
func (c *BulletinCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*CachedBulletin)
}

// SetTTL updates the cache TTL (affects future Set operations)
func (c *BulletinCache) SetTTL(ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ttl > 0 {
		c.ttl = ttl
	}
}

// Cleanup removes fully expired entries (call periodically if needed)
func (c *BulletinCache) Cleanup() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	removed := 0
	for username, cached := range c.items {
		// Remove only if both are expired
		if now.After(cached.MemoryExpiresAt) && now.After(cached.ContextExpiresAt) {
			delete(c.items, username)
			removed++
		}
	}
	return removed
}
