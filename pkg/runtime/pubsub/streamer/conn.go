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
	lock       sync.RWMutex
	streamLock sync.Mutex
	stream     rtv1pb.Dapr_SubscribeTopicEventsAlpha1Server
	closeCh    chan struct{}
	// ready is closed once the initial response is sent.
	ready            chan struct{}
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

		delete(c.publishResponses[id], c.connectionID)

		if len(c.publishResponses[id]) == 0 {
			delete(c.publishResponses, id)
		}

		c.lock.Unlock()
		// ch is not closed: the receive loop may still hold it from a lookup
		// it made before this cleanup, and a send on a closed channel panics.
	}
}

func (c *conn) notifyPublishResponse(resp *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1) {
	c.lock.RLock()
	ch, ok := c.publishResponses[resp.GetId()][c.connectionID]
	c.lock.RUnlock()

	if !ok {
		log.Errorf("no client stream expecting publish response for id %s ConnectionID%d", resp.GetId(), c.connectionID)
		return
	}

	// ch holds the one response its publish reads, so a full ch means the
	// client sent a second response for the same id.
	select {
	case ch <- resp:
	default:
		log.Warnf("dropping a duplicate publish response for id %s ConnectionID%d", resp.GetId(), c.connectionID)
	}
}
