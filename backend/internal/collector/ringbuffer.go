package collector

import "sync"

// RingBuffer is a fixed-capacity, thread-safe circular buffer of snapshots
// for one device. It backs live views (sparklines, detail charts).
type RingBuffer struct {
	mu    sync.RWMutex
	buf   []*Snapshot
	start int
	size  int
}

// NewRingBuffer creates a buffer holding up to capacity snapshots.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &RingBuffer{buf: make([]*Snapshot, capacity)}
}

// Push appends a snapshot, overwriting the oldest when full.
func (r *RingBuffer) Push(s *Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := (r.start + r.size) % len(r.buf)
	r.buf[idx] = s
	if r.size < len(r.buf) {
		r.size++
	} else {
		r.start = (r.start + 1) % len(r.buf)
	}
}

// Since returns snapshots with Ts >= since, oldest first.
func (r *RingBuffer) Since(since int64) []*Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Snapshot, 0, r.size)
	for i := 0; i < r.size; i++ {
		s := r.buf[(r.start+i)%len(r.buf)]
		if s.Ts >= since {
			out = append(out, s)
		}
	}
	return out
}

// Latest returns the newest snapshot or nil.
func (r *RingBuffer) Latest() *Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.size == 0 {
		return nil
	}
	return r.buf[(r.start+r.size-1)%len(r.buf)]
}

// Len returns the number of stored snapshots.
func (r *RingBuffer) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}
