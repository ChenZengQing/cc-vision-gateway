package cache

import (
	"sync"
	"time"
)

type Memory struct {
	mu      sync.RWMutex
	entries map[string]entry
}

type entry struct {
	Value     string
	ExpiresAt time.Time
}

func NewMemory() *Memory {
	return &Memory{entries: map[string]entry{}}
}

func (m *Memory) Get(key string) (string, bool) {
	m.mu.RLock()
	item, ok := m.entries[key]
	m.mu.RUnlock()
	if !ok || time.Now().After(item.ExpiresAt) {
		if ok {
			m.mu.Lock()
			delete(m.entries, key)
			m.mu.Unlock()
		}
		return "", false
	}
	return item.Value, true
}

func (m *Memory) Set(key, value string, ttl time.Duration) error {
	m.mu.Lock()
	m.entries[key] = entry{Value: value, ExpiresAt: time.Now().Add(ttl)}
	m.mu.Unlock()
	return nil
}

func (m *Memory) Close() error { return nil }
