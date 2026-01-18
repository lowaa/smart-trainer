package safe_map

import "sync"

type SafeMap[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]V
}

func NewSafeMap[K comparable, V any]() *SafeMap[K, V] {
	return &SafeMap[K, V]{
		m: make(map[K]V),
	}
}

func (sm *SafeMap[K, V]) Store(key K, val V) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.m[key] = val
}

func (sm *SafeMap[K, V]) Load(key K) (V, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	val, ok := sm.m[key]
	return val, ok
}

func (sm *SafeMap[K, V]) Delete(key K) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.m, key)
}

func (sm *SafeMap[K, V]) Range(f func(K, V) bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	for k, v := range sm.m {
		if !f(k, v) {
			break
		}
	}
}
