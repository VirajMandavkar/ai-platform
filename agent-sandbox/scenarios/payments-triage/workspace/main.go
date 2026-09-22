package main

import (
	"context"
	"fmt"
	"time"
)

func main() {
	fmt.Println("Payment Processor Service v1.0.0 starting...")
	queue := NewPaymentQueue()
	proc := NewProcessor(queue, 5)

	payments := []*Payment{
		{ID: "pay_1", Amount: 100.50},
		{ID: "pay_2", Amount: 250.00},
	}

	ctx := context.Background()
	_ = proc.ProcessAll(ctx, payments)

	time.Sleep(50 * time.Millisecond)
	fmt.Println("Payment Processor ready.")
}
