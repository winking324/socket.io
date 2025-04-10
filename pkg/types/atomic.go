package types

import (
	"sync/atomic"
)

// A String is an atomic string value.
// The zero value is "".
type String struct {
	_ noCopy
	v atomic.Value
}

// Load atomically loads and returns the value stored in x.
func (s *String) Load() string {
	if val, ok := s.v.Load().(string); ok {
		return val
	}
	return ""
}

// Store atomically stores val into x.
func (s *String) Store(val string) {
	s.v.Store(val)
}

// Swap atomically stores new into x and returns the previous value.
func (s *String) Swap(new string) (old string) {
	if old, ok := s.v.Swap(new).(string); ok {
		return old
	}
	return ""
}

// CompareAndSwap executes the compare-and-swap operation for the string value x.
func (s *String) CompareAndSwap(old, new string) (swapped bool) {
	return s.v.CompareAndSwap(old, new)
}
