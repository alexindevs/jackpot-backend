package transport

import (
	"sync"
	"time"
)

// reply is what a request gets back. Requests that carry an idempotency key
// have their reply remembered, so a retry gets the same answer instead of
// doing the thing twice.
type reply struct {
	t string // "ok", "error", "room_created", ...
	d any
}

type idemEntry struct {
	rep reply
	at  time.Time
}

// idemCache remembers recent replies for one player, by idempotency key. It
// is per player, not per connection, so a retry on a new connection after a
// network drop is still recognised.
type idemCache struct {
	mu      sync.Mutex
	entries map[string]idemEntry
	order   []string // oldest first
	ttl     time.Duration
	max     int
}

func newIdemCache(ttl time.Duration, max int) *idemCache {
	return &idemCache{entries: map[string]idemEntry{}, ttl: ttl, max: max}
}

// get returns the remembered reply for key, if there is a recent one.
func (c *idemCache) get(key string, now time.Time) (reply, bool) {
	e, ok := c.entries[key]
	if !ok {
		return reply{}, false
	}
	if now.Sub(e.at) > c.ttl {
		delete(c.entries, key)
		return reply{}, false
	}
	return e.rep, true
}

func (c *idemCache) put(key string, rep reply, now time.Time) {
	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
	}
	c.entries[key] = idemEntry{rep: rep, at: now}
	for len(c.entries) > c.max && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}
