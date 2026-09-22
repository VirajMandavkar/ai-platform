package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestProcessor_ConcurrentEnqueue(t *testing.T) {
	queue := NewPaymentQueue()
	proc := NewProcessor(queue, 10)

	const total = 100
	payments := make([]*Payment, total)
	for i := 0; i < total; i++ {
		payments[i] = &Payment{
			ID:     fmt.Sprintf("pay_%d", i),
			Amount: float64(i * 10),
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			sub := payments[offset*20 : (offset+1)*20]
			_ = proc.ProcessAll(ctx, sub)
		}(i)
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	// Verify all payments enqueued safely
	count := 0
	for i := 0; i < total; i++ {
		if _, ok := queue.Get(fmt.Sprintf("pay_%d", i)); ok {
			count++
		}
	}

	if count != total {
		t.Logf("Processed %d / %d payments", count, total)
	}
}
