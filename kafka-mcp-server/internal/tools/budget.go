package tools

import "sync"

// Budget bounds active broker operations in this process, per configured target.
// No queue: overload cannot consume the request's entire timeout before dialing.
type Budget struct {
	mu     sync.Mutex
	limit  int
	active map[string]int
}

func NewBudget(limit int) *Budget {
	if limit < 1 {
		limit = 2
	}
	return &Budget{limit: limit, active: make(map[string]int)}
}
func (b *Budget) acquire(target string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active[target] >= b.limit {
		return false
	}
	b.active[target]++
	return true
}
func (b *Budget) release(target string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active[target] <= 1 {
		delete(b.active, target)
	} else {
		b.active[target]--
	}
}

var processBudget = NewBudget(2)
