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

package executor

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Deactivate_concurrentInvocations(t *testing.T) {
	t.Parallel()

	for i := range 300 {
		e, f := newClaimTestExecutor(t)

		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 4 {
			wg.Go(func() {
				<-start
				_, err := e.InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
				assert.NoError(t, err, "iteration %d", i)
			})
		}
		wg.Go(func() {
			<-start
			assert.NoError(t, e.Deactivate(t.Context()), "iteration %d", i)
		})
		close(start)
		wg.Wait()

		assert.True(t, e.closed.Load(), "iteration %d", i)
		_, ok := f.table.Load("abc")
		assert.False(t, ok, "iteration %d", i)
	}
}

func Test_callGroup(t *testing.T) {
	t.Parallel()

	t.Run("wait returns immediately when idle", func(t *testing.T) {
		t.Parallel()

		var g callGroup
		done := make(chan struct{})
		go func() {
			g.wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "wait blocked with no calls")
		}
	})

	t.Run("wait returns once in-flight calls finish", func(t *testing.T) {
		t.Parallel()

		var g callGroup
		g.add()
		g.add()

		done := make(chan struct{})
		go func() {
			g.wait()
			close(done)
		}()

		g.done()
		select {
		case <-done:
			require.FailNow(t, "wait returned with a call in flight")
		case <-time.After(50 * time.Millisecond):
		}

		g.done()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "wait did not return")
		}
	})

	t.Run("add and done may run concurrently with wait", func(t *testing.T) {
		t.Parallel()

		for range 200 {
			var g callGroup
			var wg sync.WaitGroup
			start := make(chan struct{})
			for range 4 {
				wg.Go(func() {
					<-start
					g.add()
					g.done()
				})
			}
			wg.Go(func() {
				<-start
				g.wait()
			})
			close(start)
			wg.Wait()
		}
	})
}
