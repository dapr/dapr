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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
)

// Load is a load of instance starts which StartConcurrently runs on several
// goroutines.
type Load struct {
	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup

	lock sync.Mutex
	ids  []api.InstanceID

	// recorded counts the instances each goroutine has recorded.
	recorded []atomic.Int64
}

// StartConcurrently runs body in a loop on each of n goroutines, with the
// goroutine and the iteration numbers, until the returned load's Stop is
// called. When body returns true, the instance ID it returns is recorded.
// The goroutines are also stopped and waited for in t's cleanup, so a failed
// assertion before Stop does not leave them running past the test.
func StartConcurrently(t *testing.T, n int, body func(g, i int) (api.InstanceID, bool)) *Load {
	t.Helper()

	l := &Load{
		done:     make(chan struct{}),
		recorded: make([]atomic.Int64, n),
	}
	for g := range n {
		l.wg.Go(func() {
			for i := 0; ; i++ {
				select {
				case <-l.done:
					return
				default:
				}

				if id, ok := body(g, i); ok {
					l.lock.Lock()
					l.ids = append(l.ids, id)
					l.lock.Unlock()
					l.recorded[g].Add(1)
				}
			}
		})
	}

	t.Cleanup(l.halt)

	return l
}

// Stop stops the goroutines, waits for them, and returns the recorded
// instance IDs.
func (l *Load) Stop() []api.InstanceID {
	l.halt()
	return l.ids
}

// WaitStarted waits until each goroutine has recorded n more instances since
// the call, so the load is seen to run on through every goroutine. The wait
// allows a goroutine a call held up for several seconds, as by a drain.
func (l *Load) WaitStarted(t *testing.T, n int) {
	t.Helper()

	from := make([]int64, len(l.recorded))
	for g := range l.recorded {
		from[g] = l.recorded[g].Load()
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		for g := range l.recorded {
			assert.GreaterOrEqual(c, l.recorded[g].Load(), from[g]+int64(n), "instances recorded by goroutine %d", g)
		}
	}, time.Second*20, time.Millisecond*10)
}

// halt stops the goroutines and waits for them, once.
func (l *Load) halt() {
	l.once.Do(func() {
		close(l.done)
		l.wg.Wait()
	})
}
