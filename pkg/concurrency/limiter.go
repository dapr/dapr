/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// CODE ATTRIBUTION: https://github.com/korovkin/limiter
// Modified to accept a parameter to the executed job
package concurrency

import (
	"sync/atomic"
)

const (
	// DefaultLimit is the default concurrency limit.
	DefaultLimit = 100
)

// Limiter object.
type Limiter struct {
	limit         int
	tickets       chan int
	numInProgress int32
}

// NewLimiter allocates a new ConcurrencyLimiter.
func NewLimiter(limit int) *Limiter {
	if limit <= 0 {
		limit = DefaultLimit
	}

	// allocate a limiter instance
	c := &Limiter{
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
// else wait until a go routine becomes available.
func (c *Limiter) Execute(job func(param interface{}), param interface{}) int {
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
func (c *Limiter) Wait() {
	for i := 0; i < c.limit; i++ {
		<-c.tickets
	}
}
