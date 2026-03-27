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

package pool

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/diagridio/go-etcd-cron/api"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop/fake"
)

func testJob() *internalsv1pb.JobEvent {
	return &internalsv1pb.JobEvent{Metadata: &schedulerv1pb.JobMetadata{}}
}

func TestTrigger_ReturnsWhenCallbackFires(t *testing.T) {
	p := &Pool{
		readyCh: make(chan struct{}),
		connsLoop: fake.New[loops.Event]().WithEnqueue(func(e loops.Event) {
			go func() {
				e.(*loops.TriggerRequest).ResultFn(api.TriggerResponseResult_SUCCESS)
			}()
		}),
	}
	close(p.readyCh)

	result := p.Trigger(context.Background(), testJob())
	assert.Equal(t, api.TriggerResponseResult_SUCCESS, result)
}

func TestTrigger_BlocksWhenCallbackNeverFires(t *testing.T) {
	p := &Pool{
		readyCh:   make(chan struct{}),
		connsLoop: fake.New[loops.Event](),
	}
	close(p.readyCh)

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Trigger(context.Background(), testJob())
	}()

	select {
	case <-done:
		t.Fatal("Trigger returned but callback was never called")
	case <-time.After(time.Second):
	}
}

func TestTrigger_ReturnsUndeliverableOnContextCancellation(t *testing.T) {
	p := &Pool{
		readyCh:   make(chan struct{}),
		connsLoop: fake.New[loops.Event](),
	}
	close(p.readyCh)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan api.TriggerResponseResult, 1)
	go func() {
		done <- p.Trigger(ctx, testJob())
	}()

	cancel()

	select {
	case result := <-done:
		assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, result)
	case <-time.After(2 * time.Second):
		t.Fatal("Pool.Trigger blocks forever after ctx.Done()")
	}
}

func TestTrigger_LateCallbackDoesNotCorruptNextTrigger(t *testing.T) {
	// After ctx cancellation, ResultFn may fire later (e.g., stream
	// shutdown). The late write must not corrupt a subsequent Trigger
	// call that reuses the same respCh from the pool.
	var captured []*loops.TriggerRequest
	var mu sync.Mutex

	p := &Pool{
		readyCh: make(chan struct{}),
		connsLoop: fake.New[loops.Event]().WithEnqueue(func(e loops.Event) {
			mu.Lock()
			captured = append(captured, e.(*loops.TriggerRequest))
			mu.Unlock()
		}),
	}
	close(p.readyCh)

	// Trigger 1: cancel immediately.
	ctx1, cancel1 := context.WithCancel(context.Background())
	cancel1()
	result1 := p.Trigger(ctx1, testJob())
	assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, result1)

	// Give the drain goroutine time to block on <-respCh.
	time.Sleep(50 * time.Millisecond)

	// Trigger 2: should get its own result, not Trigger 1's late callback.
	done := make(chan api.TriggerResponseResult, 1)
	go func() {
		done <- p.Trigger(context.Background(), testJob())
	}()

	// Give Trigger 2 time to enqueue.
	time.Sleep(50 * time.Millisecond)

	// Fire Trigger 1's late ResultFn (simulates handleShutdown).
	mu.Lock()
	require.GreaterOrEqual(t, len(captured), 1)
	captured[0].ResultFn(api.TriggerResponseResult_FAILED)
	mu.Unlock()

	// Fire Trigger 2's ResultFn.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	require.Len(t, captured, 2)
	captured[1].ResultFn(api.TriggerResponseResult_SUCCESS)
	mu.Unlock()

	select {
	case result2 := <-done:
		assert.Equal(t, api.TriggerResponseResult_SUCCESS, result2,
			"Trigger 2 should get SUCCESS, not Trigger 1's late FAILED")
	case <-time.After(2 * time.Second):
		t.Fatal("Trigger 2 never returned")
	}
}

func TestTrigger_ConcurrentCancellationsDoNotLeak(t *testing.T) {
	// Many triggers cancelled rapidly should not leak goroutines or
	// corrupt the respCh pool.
	p := &Pool{
		readyCh:   make(chan struct{}),
		connsLoop: fake.New[loops.Event]().WithEnqueue(func(e loops.Event) {}),
	}
	close(p.readyCh)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := p.Trigger(ctx, testJob())
			assert.Equal(t, api.TriggerResponseResult_UNDELIVERABLE, result)
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("100 concurrent cancelled triggers did not complete in 5s")
	}
}
