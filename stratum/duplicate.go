package stratum

import (
	"fmt"
	"strings"
	"sync"
)

// defaultShareCacheSize is how many recent shares are remembered per session so
// that a resubmitted share is rejected instead of being counted twice.
const defaultShareCacheSize = 4096

// shareKey identifies a submitted share within a session.
func shareKey(jobID, extranonce2, nTime, nonce string, versionBits interface{}) string {
	return strings.Join([]string{jobID, extranonce2, nTime, nonce, fmt.Sprintf("%v", versionBits)}, ":")
}

// shareDeduper is a fixed-size set of recently submitted share keys.
type shareDeduper struct {
	mu   sync.Mutex
	max  int
	next int
	seen map[string]struct{}
	ring []string
}

func newShareDeduper(max int) *shareDeduper {
	if max <= 0 {
		max = defaultShareCacheSize
	}
	return &shareDeduper{
		max:  max,
		seen: make(map[string]struct{}, max),
	}
}

// SeenOrAdd reports whether key was already submitted and remembers it otherwise.
// The oldest key is forgotten once the cache is full.
func (d *shareDeduper) SeenOrAdd(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.seen[key]; ok {
		return true
	}

	if len(d.ring) < d.max {
		d.ring = append(d.ring, key)
	} else {
		delete(d.seen, d.ring[d.next])
		d.ring[d.next] = key
		d.next = (d.next + 1) % d.max
	}
	d.seen[key] = struct{}{}
	return false
}
