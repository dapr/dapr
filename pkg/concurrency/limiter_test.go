/*
Copyright 2023 The Dapr Authors
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

package concurrency

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLimiter(t *testing.T) {
	// Create a new limiter with a limit of 3
	limiter := NewLimiter(3)

	// Execute some jobs with the limiter
	for i := 0; i < 10; i++ {
		limiter.Execute(func(param interface{}) {
			assert.LessOrEqual(t, atomic.LoadInt32(&limiter.numInProgress), int32(3), "more than 3 tasks are running concurrently")
			// Simulate some work
			time.Sleep(10 * time.Millisecond)
		}, i)
	}
	// Wait for all tasks to complete
	limiter.Wait()

	assert.Equal(t, int32(0), limiter.numInProgress, "numInProgress is not 0")
}
