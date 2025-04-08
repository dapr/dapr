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
	lock              sync.RWMutex
	streamLock        sync.Mutex
	stream            rtv1pb.Dapr_SubscribeTopicEventsAlpha1Server
	publishResponses3 map[string]map[rtpubsub.ConnectionID]chan *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1
	closeCh           chan struct{}
	closed            atomic.Bool
	connectionID      rtpubsub.ConnectionID
}

func (c *conn) registerPublishResponse(id string) (chan *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1, func()) {
	ch := make(chan *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1, 1)
	log.Warnf("Lock registerPublishResponse messageId %s ConnectionID%d", id, c.connectionID)
	c.lock.Lock()

	if c.publishResponses3[id] == nil {
		c.publishResponses3[id] = make(map[rtpubsub.ConnectionID]chan *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1)
	}
	c.publishResponses3[id][c.connectionID] = ch

	c.lock.Unlock()
	log.Warnf("Unlock registerPublishResponse messageId %s ConnectionID%d", id, c.connectionID)
	return ch, func() {
		log.Warnf("Lock registerPublishResponse defer messageId %s ConnectionID%d", id, c.connectionID)
		c.lock.Lock()

		delete(c.publishResponses3[id], c.connectionID)
		if len(c.publishResponses3[id]) == 0 {
			delete(c.publishResponses3, id)
		}

		c.lock.Unlock()
		// Ensure channel is closed to prevent goroutine leaks
		select {
		case <-ch: // drain if there's a value
		default:
		}
		close(ch)
		log.Warnf("Unlock registerPublishResponse defer messageId %s ConnectionID%d", id, c.connectionID)
	}
}

func (c *conn) notifyPublishResponse(resp *rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1) {
	log.Warnf("Lock notifyPublishResponse messageId %s ConnectionID%d", resp.GetId(), c.connectionID)
	c.lock.RLock()
	ch, ok := c.publishResponses3[resp.GetId()][c.connectionID]
	c.lock.RUnlock()
	log.Warnf("Unlock notifyPublishResponse messageId %s ConnectionID%d", resp.GetId(), c.connectionID)

	if !ok {
		log.Errorf("no client stream expecting publish response for id %s ConnectionID%d", resp.GetId(), c.connectionID)
		return
	}

	ch <- resp
}
