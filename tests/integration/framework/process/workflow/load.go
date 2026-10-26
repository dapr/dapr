/*
Copyright 2026 The Dapr Authors
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

package workflow

import (
	"sync"
	"testing"

	"github.com/dapr/durabletask-go/api"
)

// StartConcurrently runs body in a loop on each of n goroutines, with the
// goroutine and the iteration numbers, until the returned stop is called.
// When body returns true, the instance ID it returns is recorded; stop waits
// for the goroutines and returns the recorded IDs. The goroutines are also
// stopped and waited for in t's cleanup, so a failed assertion before stop
// does not leave them running past the test.
func StartConcurrently(t *testing.T, n int, body func(g, i int) (api.InstanceID, bool)) (stop func() []api.InstanceID) {
	t.Helper()

	done := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	var lock sync.Mutex
	var ids []api.InstanceID
	for g := range n {
		wg.Go(func() {
			for i := 0; ; i++ {
				select {
				case <-done:
					return
				default:
				}

				if id, ok := body(g, i); ok {
					lock.Lock()
					ids = append(ids, id)
					lock.Unlock()
				}
			}
		})
	}

	halt := func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}
	t.Cleanup(halt)

	return func() []api.InstanceID {
		halt()
		return ids
	}
}
