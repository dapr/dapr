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

package namespaces

import (
	"testing"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// Test_NamespaceDuplicateCloseStream injects duplicate close events for one
// stream and asserts the namespace survives with its remaining live stream
// intact. A stray duplicate close must not double-decrement the namespace
// connection count: that deletes the namespace while live streams remain,
// dropping every stream and deliverable prefix in it (the mechanism behind
// the minutes-long reminder-delivery outage after a scheduler restart).
func Test_NamespaceDuplicateCloseStream(t *testing.T) {
	t.Parallel()

	suite := newSuite(t)

	sc1 := newClientServer(t)
	sc2 := newClientServer(t)

	suite.connLoop.Enqueue(&loops.ConnAdd{
		Channel: sc1.serverstream,
		Cancel:  sc1.closeserver,
		Request: &schedulerv1pb.WatchJobsRequestInitial{
			AppId:     "app1",
			Namespace: "ns1",
		},
	})
	suite.connLoop.Enqueue(&loops.ConnAdd{
		Channel: sc2.serverstream,
		Cancel:  sc2.closeserver,
		Request: &schedulerv1pb.WatchJobsRequestInitial{
			AppId:     "app2",
			Namespace: "ns1",
		},
	})

	// Both adds processed once both app prefixes are registered.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		suite.prefixesMu.Lock()
		defer suite.prefixesMu.Unlock()
		assert.ElementsMatch(c, []string{
			"app||ns1||app1||", "app||ns1||app2||",
		}, *suite.prefixes)
	}, time.Second*5, time.Millisecond*10)

	// Duplicate close for app1's stream (idx 0): in production both the recv
	// loop exit and every failed Send raced to emit one, and any future
	// regression must not collapse the namespace.
	suite.connLoop.Enqueue(&loops.ConnCloseStream{StreamIDx: 0, Namespace: "ns1"})
	suite.connLoop.Enqueue(&loops.ConnCloseStream{StreamIDx: 0, Namespace: "ns1"})

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, sc1.serverclosed.Load())
	}, time.Second*5, time.Millisecond*10)

	// app2's live stream must survive the duplicate close.
	recvCh := make(chan error, 1)
	go func() {
		_, err := sc2.clientstream.Recv()
		recvCh <- err
	}()

	suite.connLoop.Enqueue(&loops.TriggerRequest{
		ResultFn: func(r api.TriggerResponseResult) {},
		Job: &internalsv1pb.JobEvent{
			Key:  "app||ns1||app2||job1",
			Name: "job1",
			Metadata: &schedulerv1pb.JobMetadata{
				AppId:     "app2",
				Namespace: "ns1",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Job{
						Job: new(schedulerv1pb.TargetJob),
					},
				},
			},
		},
	})

	select {
	case err := <-recvCh:
		require.NoError(t, err, "live stream was torn down by the duplicate close for its sibling")
	case <-time.After(time.Second * 5):
		t.Fatal("trigger was not delivered: namespace routing lost after duplicate close")
	}

	require.False(t, sc2.serverclosed.Load(), "live stream closed by duplicate close event")
	require.Nil(t, suite.cancelCalled.Load(), "duplicate close escalated to pool teardown")

	suite.connLoop.Close(new(loops.Shutdown))
}
