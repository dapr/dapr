// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

// CODE ATTRIBUTION: https://github.com/korovkin/limiter
// Modified to accept a parameter to the executed job
package concurrency

import (
	"sync/atomic"
)

const (
	// DefaultLimit is the default concurrency limit
	DefaultLimit = 100
)

// ConcurrencyLimiter object
type ConcurrencyLimiter struct {
	limit         int
	tickets       chan int
	numInProgress int32
}

// NewConcurrencyLimiter allocates a new ConcurrencyLimiter
func NewConcurrencyLimiter(limit int) *ConcurrencyLimiter {
	if limit <= 0 {
		limit = DefaultLimit
	}

	// allocate a limiter instance
	c := &ConcurrencyLimiter{
		limit:   limit,
		tickets: make(chan int, limit),
	}

	// allocate the tickets:
	for i := 0; i < c.limit; i++ {
		c.tickets <- i
	}

	return c
}

// Execute adds a function to the execution queue.
// if num of go routines allocated by this instance is < limit
// launch a new go routine to execute job
// else wait until a go routine becomes available
func (c *ConcurrencyLimiter) Execute(job func(param interface{}), param interface{}) int {
	ticket := <-c.tickets
	atomic.AddInt32(&c.numInProgress, 1)
	go func(param interface{}) {
		defer func() {
			c.tickets <- ticket
			atomic.AddInt32(&c.numInProgress, -1)

		}()

		// run the job
		job(param)
	}(param)
	return ticket
}

// Wait will block all the previously Executed jobs completed running.
//
// IMPORTANT: calling the Wait function while keep calling Execute leads to
//            un-desired race conditions
func (c *ConcurrencyLimiter) Wait() {
	for i := 0; i < c.limit; i++ {
		_ = <-c.tickets
	}
}
