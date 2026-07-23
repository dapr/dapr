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

package stream

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// Test_StreamDeadSendClose covers the dead-stream close path: a failed Send
// must resolve the trigger's result promptly as UNDELIVERABLE (so the cron
// engine redelivers to a live stream instead of parking the trigger until the
// stream is reaped), and the stream must emit exactly one ConnCloseStream no
// matter how many sends fail after death. Duplicate close events corrupt the
// per-namespace connection accounting and tear down live streams.
func Test_StreamDeadSendClose(t *testing.T) {
	t.Parallel()

	suite := newSuite(t)

	// Kill the server side of the stream: the recv loop exits and emits the
	// stream's single close event.
	suite.closeserver()
	suite.expectEvent(t, &loops.ConnCloseStream{
		StreamIDx: 123,
		Namespace: "ns",
	})

	// Triggers routed to the dead stream fail their Send.
	var calls1, calls2 atomic.Int32
	var res1, res2 atomic.Pointer[api.TriggerResponseResult]
	suite.streamLoop.Enqueue(&loops.TriggerRequest{
		Job: &internalsv1pb.JobEvent{
			Key: "job-key-1", Name: "job-name-1",
		},
		ResultFn: func(res api.TriggerResponseResult) {
			calls1.Add(1)
			res1.Store(&res)
		},
	})
	suite.streamLoop.Enqueue(&loops.TriggerRequest{
		Job: &internalsv1pb.JobEvent{
			Key: "job-key-2", Name: "job-name-2",
		},
		ResultFn: func(res api.TriggerResponseResult) {
			calls2.Add(1)
			res2.Store(&res)
		},
	})

	// The failed sends must resolve UNDELIVERABLE promptly, well before the
	// stream is shut down by the connections loop.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		ptr1, ptr2 := res1.Load(), res2.Load()
		if !assert.NotNil(c, ptr1, "trigger 1 not resolved after failed send") ||
			!assert.NotNil(c, ptr2, "trigger 2 not resolved after failed send") {
			return
		}
		assert.Equal(c, api.TriggerResponseResult_UNDELIVERABLE, *ptr1)
		assert.Equal(c, api.TriggerResponseResult_UNDELIVERABLE, *ptr2)
	}, time.Second*5, time.Millisecond*10)

	// No further close events: the recv-loop exit and every failed send race
	// to close the same stream and must dedupe to the single event above.
	select {
	case ev := <-suite.nsLoop.Events():
		t.Fatalf("unexpected duplicate close event for dead stream: %#v", ev)
	case <-time.After(time.Second):
	}

	suite.streamLoop.Close(new(loops.StreamShutdown))

	// The shutdown drain must not double-resolve the already-resolved
	// triggers.
	require.Equal(t, int32(1), calls1.Load(), "trigger 1 resolved more than once")
	require.Equal(t, int32(1), calls2.Load(), "trigger 2 resolved more than once")
}
