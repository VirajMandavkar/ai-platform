package limiter

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBucket_ConcurrencyAndPriority(t *testing.T) {
	b := NewBucket(2) // Allow 2 active requests

	// 1. Acquire 2 slots immediately
	ctx := context.Background()
	if err := b.Acquire(ctx, StandardPriority); err != nil {
		t.Fatalf("acquire slot 1: %v", err)
	}
	if err := b.Acquire(ctx, StandardPriority); err != nil {
		t.Fatalf("acquire slot 2: %v", err)
	}

	// 2. Queue standard priority waiter
	var order []string
	var mu sync.Mutex

	go func() {
		_ = b.Acquire(ctx, StandardPriority)
		mu.Lock()
		order = append(order, "standard")
		mu.Unlock()
		b.Release()
	}()

	time.Sleep(20 * time.Millisecond)

	// 3. Queue high priority waiter (should jump ahead of standard)
	go func() {
		_ = b.Acquire(ctx, HighPriority)
		mu.Lock()
		order = append(order, "high")
		mu.Unlock()
		b.Release()
	}()

	time.Sleep(20 * time.Millisecond)

	// Release 1 slot
	b.Release()
	time.Sleep(50 * time.Millisecond)

	// Release 2nd slot
	b.Release()
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 {
		t.Fatalf("expected 2 waiters to finish, got %d", len(order))
	}
	if order[0] != "high" {
		t.Errorf("expected high priority waiter to finish first, got %s", order[0])
	}
}

func TestBucket_ContextCancellation(t *testing.T) {
	b := NewBucket(1)
	ctx := context.Background()
	_ = b.Acquire(ctx, StandardPriority)

	cancelCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := b.Acquire(cancelCtx, StandardPriority)
	if err == nil {
		t.Errorf("expected context cancellation error, got nil")
	}
	if time.Since(start) < 25*time.Millisecond {
		t.Errorf("expected wait before timeout")
	}
}
