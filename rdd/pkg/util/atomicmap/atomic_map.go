// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package atomicmap provides a thread-safe map implementation using a mutex.
package atomicmap

import "sync"

// AtomicMap is a map that wraps a mutex around map operations; the zero value
// is a usable empty map.  It wraps the simple case of using a map with a mutex,
// but it is not always the correct thing to use.
type AtomicMap[K comparable, V any] struct {
	values map[K]V
	mutex  sync.Mutex
}

// Store the given value into the map using the given key, overwriting any
// existing value if the key already exists.
func (m *AtomicMap[K, V]) Store(key K, value V) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.values == nil {
		m.values = make(map[K]V)
	}
	m.values[key] = value
}

// Load from the map, returning the value and a boolean indicating if the key
// exists.  [AtomicMap.LoadAndDelete] is preferred over using [AtomicMap.Load]
// immediately followed by [AtomicMap.Delete].
func (m *AtomicMap[K, V]) Load(key K) (V, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	result, ok := m.values[key]
	return result, ok
}

// LoadAndDelete retrieves the value for the given key and then deletes the key
// from the map.  Returns the value and a boolean indicating if the key existed.
func (m *AtomicMap[K, V]) LoadAndDelete(key K) (V, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	result, ok := m.values[key]
	delete(m.values, key)
	return result, ok
}

// Delete removes the value for the given key from the map.
// [AtomicMap.LoadAndDelete] is preferred over using [AtomicMap.Load]
// immediately followed by [AtomicMap.Delete].
func (m *AtomicMap[K, V]) Delete(key K) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	delete(m.values, key)
}

// Modify an entry atomically using the given function.  The function is passed
// the existing value and a boolean indicating if the key exists.  It should
// return the new value; if the boolean is false, the key will be deleted from
// the map.  Returns the modified value (which may have been deleted).
func (m *AtomicMap[K, V]) Modify(key K, mutate func(value V, exists bool) (V, bool)) V {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	existing, ok := m.values[key]
	newValue, shouldStore := mutate(existing, ok)
	if shouldStore {
		if m.values == nil {
			m.values = make(map[K]V)
		}
		m.values[key] = newValue
	} else if ok {
		delete(m.values, key)
	}
	return newValue
}
