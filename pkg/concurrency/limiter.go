// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

// CODE ATTRIBUTION: https://github.com/korovkin/limiter
// Modified to accept a parameter to the executed job
package concurrency

const (
	// DefaultLimit is the default concurrency limit
	DefaultLimit = 100
)

// Limiter object
type Limiter struct {
	limit   int
	tickets chan struct{}
}

// NewLimiter allocates a new ConcurrencyLimiter
func NewLimiter(limit int) *Limiter {
	if limit <= 0 {
		limit = DefaultLimit
	}

	// allocate a limiter instance
	c := &Limiter{
		limit:   limit,
		tickets: make(chan struct{}, limit),
	}

	// allocate the tickets:
	for i := 0; i < c.limit; i++ {
		c.tickets <- struct{}{}
	}

	return c
}

// Execute adds a function to the execution queue.
// if num of go routines allocated by this instance is < limit
// launch a new go routine to execute job
// else wait until a go routine becomes available
func (c *Limiter) Execute(job func(param interface{}), param interface{}) {
	<-c.tickets
	go func(param interface{}) {
		// run the job
		job(param)
		c.tickets <- struct{}{}
	}(param)
}

// Wait will block all the previously Executed jobs completed running.
//
// IMPORTANT: calling the Wait function while keep calling Execute leads to
//            un-desired race conditions
func (c *Limiter) Wait() {
	for i := 0; i < c.limit; i++ {
		<-c.tickets
	}
}
