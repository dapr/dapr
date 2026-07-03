/*
Copyright 2024 The Dapr Authors
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

package streamer

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/kit/logger"

	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// TestNotifyPublishResponseDelivers is a happy-path guard: the single expected
// response for a registered id is delivered to the channel that Publish waits
// on. Because notifyPublishResponse sends non-blocking, this ensures the fix
// does not silently drop the legitimate response.
func TestNotifyPublishResponseDelivers(t *testing.T) {
	const id = "test-id"

	c := &conn{publishResponses: make(PublishResponses)}
	ch, cleanup := c.registerPublishResponse(id)
	t.Cleanup(cleanup)

	resp := &rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1{Id: id}
	c.notifyPublishResponse(resp)

	select {
	case got := <-ch:
		assert.Same(t, resp, got)
	case <-time.After(time.Second):
		require.Fail(t, "expected response was not delivered")
	}
}

// TestNotifyPublishResponseConcurrentCleanup exercises the race between
// notifyPublishResponse delivering a response and the cleanup closure closing
// the same per-request channel. cleanup() runs whenever Publish returns
// (including its early ctx.Done()/closeCh exits) while recvLoop can be
// concurrently calling notifyPublishResponse for the same id. When the send and
// the close are not serialized, the send can land on an already-closed channel
// and panic with "send on closed channel", crashing the sidecar. Serializing
// the send (read lock) against the close (write lock) makes them mutually
// exclusive. Run with -race.
func TestNotifyPublishResponseConcurrentCleanup(t *testing.T) {
	log.SetOutputLevel(logger.FatalLevel)
	defer log.SetOutputLevel(logger.InfoLevel)

	const rounds = 50
	const pairs = 200
	const id = "test-id"

	for range rounds {
		type registration struct {
			c       *conn
			cleanup func()
		}

		regs := make([]registration, pairs)
		for i := range regs {
			c := &conn{publishResponses: make(PublishResponses)}
			_, cleanup := c.registerPublishResponse(id)
			regs[i] = registration{c: c, cleanup: cleanup}
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(len(regs) * 2)

		for _, reg := range regs {
			// recvLoop delivering the app's response.
			go func() {
				defer wg.Done()
				<-start
				reg.c.notifyPublishResponse(&rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1{Id: id})
			}()

			// Publish's deferred cleanup closing the channel once it returns.
			go func() {
				defer wg.Done()
				<-start
				reg.cleanup()
			}()
		}

		close(start)
		wg.Wait()
	}
}

// TestNotifyPublishResponseDuplicateResponseNoDeadlock covers the case where a
// second response for the same id arrives after the cap-1 buffer is already
// full and the matching Publish has returned without draining it (an early
// ctx.Done()/closeCh exit, or a buggy app that sends twice). The delivery runs
// under the read lock, so a blocking send would park while holding it and
// deadlock cleanup's write-lock close. Because the send is non-blocking, the
// duplicate is dropped and cleanup can always complete. The timeout guard fails
// the test if the send parks and wedges cleanup.
func TestNotifyPublishResponseDuplicateResponseNoDeadlock(t *testing.T) {
	log.SetOutputLevel(logger.FatalLevel)
	defer log.SetOutputLevel(logger.InfoLevel)

	const id = "test-id"

	c := &conn{publishResponses: make(PublishResponses)}
	_, cleanup := c.registerPublishResponse(id)

	// First response fills the cap-1 buffer; Publish has already returned so no
	// one drains it.
	c.notifyPublishResponse(&rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1{Id: id})

	done := make(chan struct{})
	go func() {
		// Second, duplicate response on the now-full buffer, concurrent with
		// cleanup closing the channel.
		c.notifyPublishResponse(&rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1{Id: id})
		cleanup()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.Fail(t, "notifyPublishResponse/cleanup deadlocked on a duplicate response")
	}
}
