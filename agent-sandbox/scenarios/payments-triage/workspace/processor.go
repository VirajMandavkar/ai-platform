package main

import (
	"context"
	"sync"
	"time"
)

type Payment struct {
	ID     string
	Amount float64
	Status string
}

type PaymentQueue struct {
	// BUG: Unprotected map causing concurrent read/write panic
	items map[string]*Payment
}

func NewPaymentQueue() *PaymentQueue {
	return &PaymentQueue{
		items: make(map[string]*Payment),
	}
}

func (q *PaymentQueue) Enqueue(p *Payment) {
	// BUG: Data race on concurrent access
	q.items[p.ID] = p
}

func (q *PaymentQueue) Get(id string) (*Payment, bool) {
	p, ok := q.items[id]
	return p, ok
}

type Processor struct {
	queue   *PaymentQueue
	workers int
	wg      sync.WaitGroup
}

func NewProcessor(q *PaymentQueue, workers int) *Processor {
	return &Processor{
		queue:   q,
		workers: workers,
	}
}

func (pr *Processor) ProcessAll(ctx context.Context, payments []*Payment) error {
	for _, p := range payments {
		// BUG: Unbounded goroutine spawning causing massive memory leaks under load
		go func(pay *Payment) {
			pr.queue.Enqueue(pay)
			// Simulate processing work
			time.Sleep(10 * time.Millisecond)
			pay.Status = "COMPLETED"
		}(p)
	}
	return nil
}
