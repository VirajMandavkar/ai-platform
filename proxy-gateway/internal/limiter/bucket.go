package limiter

import (
	"container/heap"
	"context"
	"sync"
)

// Priority defines how important the request is.
type Priority int

const (
	StandardPriority Priority = iota // 0: New manual prompts from the student
	HighPriority                     // 1: Active CLI agent loops (don't break their flow)
)

// Waiter represents a single HTTP request paused in the queue
type Waiter struct {
	Priority Priority
	Ready    chan struct{} // The HTTP Handler blocks on this channel waiting for a signal
	Index    int           // Required by container/ heap to track the elements position
}

// WaitQueue is a priority queue fo Waiters.
type WaitQueue []*Waiter

// Bucket is the global rate limiter
type Bucket struct {
	mu     sync.Mutex
	limit  int       // Max concurrent requests allowed
	active int       // How many requests are currently in-flight to AI model
	queue  WaitQueue // This will implement the container/ heap interface
}

// Push adds an item to the slice. The container/heap package handles the re-ordering.
func (wq *WaitQueue) Push(x interface{}) {
	n := len(*wq)
	item := x.(*Waiter)
	item.Index = n
	*wq = append(*wq, item)
}

// Pop removes and returns the last element
// container/heap handles swapping the top priority element to the end before calling this
func (wq *WaitQueue) Pop() any {
	old := *wq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil  // Avoid memory leak
	item.Index = -1 // Safety mark indicating it's no longer in the heap
	*wq = old[0 : n-1]
	return item
}

// Len returns the number of waiting requests
func (wq WaitQueue) Len() int {
	return len(wq)
}

// Less determines priority order.
// We want higher priority values popped first (Max-Heap behavior).
func (wq WaitQueue) Less(i, j int) bool {
	return wq[i].Priority > wq[j].Priority
}

// Swap exchange elements and keeps track of their indices
func (wq WaitQueue) Swap(i, j int) {
	wq[i], wq[j] = wq[j], wq[i]
	wq[i].Index = i
	wq[j].Index = j
}

// Acquire attempts to get a slot to talk to the AI.
// If the bucket is full, it puts the request to sleep until a slot opens.
func (b *Bucket) Acquire(ctx context.Context, p Priority) error {
	b.mu.Lock()

	if b.active < b.limit {
		b.active++
		b.mu.Unlock()
		return nil
	}

	waiter := &Waiter{
		Priority: p,
		Ready:    make(chan struct{}, 1),
	}

	heap.Push(&b.queue, waiter)
	b.mu.Unlock()

	select {
	case <-waiter.Ready:
		return nil
	case <-ctx.Done():
		b.mu.Lock()
		select {
		case <-waiter.Ready:
			b.mu.Unlock()
			b.Release()
		default:
			if waiter.Index >= 0 {
				heap.Remove(&b.queue, waiter.Index)
				b.mu.Unlock()
			} else {
				// It was just popped by Release() but hasn't received the channel message yet.
				b.mu.Unlock()
				<-waiter.Ready
				b.Release()
			}
		}

		return ctx.Err()
	}
}

// Release frees up a slot. It either wakes up the next waiting request,
// or it reduces the active count if the line is empty.
func (b *Bucket) Release() {
	b.mu.Lock()

	var nextWaiter *Waiter
	if len(b.queue) > 0 {
		nextWaiter = heap.Pop(&b.queue).(*Waiter)
	} else {
		b.active--
	}

	b.mu.Unlock()

	if nextWaiter != nil {
		nextWaiter.Ready <- struct{}{}
	}
}

// NewBucket initializes a rate-limiting bucket with max concurrency limit.
func NewBucket(limit int) *Bucket {
	b := &Bucket{
		limit: limit,
		queue: make(WaitQueue, 0),
	}
	heap.Init(&b.queue)
	return b
}
