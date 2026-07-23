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
	"sync/atomic"

	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

type conn struct {
	lock             sync.RWMutex
	streamLock       sync.Mutex
	stream           rtv1pb.Dapr_SubscribeTopicEventsAlpha1Server
	closeCh          chan struct{}
	closed           atomic.Bool
	connectionID     rtpubsub.ConnectionID
	publishResponses PublishResponses
}

func (c *conn) registerPublishResponse(id string) (chan *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1, func()) {
	ch := make(chan *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1, 1)

	c.lock.Lock()

	if c.publishResponses[id] == nil {
		c.publishResponses[id] = make(ConnectionChannel)
	}

	c.publishResponses[id][c.connectionID] = ch

	c.lock.Unlock()

	return ch, func() {
		c.lock.Lock()
		defer c.lock.Unlock()

		delete(c.publishResponses[id], c.connectionID)

		if len(c.publishResponses[id]) == 0 {
			delete(c.publishResponses, id)
		}

		// Ensure channel is closed to prevent goroutine leaks. Closing under the
		// write lock keeps it mutually exclusive with the send in
		// notifyPublishResponse (which holds the read lock), so a response
		// delivered concurrently with cleanup can never send on a closed channel.
		select {
		case <-ch: // drain if there's a value
		default:
		}

		close(ch)
	}
}

func (c *conn) notifyPublishResponse(resp *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	ch, ok := c.publishResponses[resp.GetId()][c.connectionID]
	if !ok {
		log.Errorf("no client stream expecting publish response for id %s ConnectionID%d", resp.GetId(), c.connectionID)
		return
	}

	// Deliver under the read lock so the send is mutually exclusive with the
	// close in cleanup (which holds the write lock): the channel can never be
	// closed between the lookup and the send. The send is non-blocking so it can
	// never park while holding the read lock and deadlock cleanup's write-lock
	// close; the cap-1 buffer holds the single expected response, and any extra
	// same-id response (or one whose Publish already returned) is dropped rather
	// than blocking.
	select {
	case ch <- resp:
	default:
	}
}
