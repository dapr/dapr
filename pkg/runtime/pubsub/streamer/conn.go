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
	"context"
	"sync"

	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"google.golang.org/appengine/log"
)

type conn struct {
	lock             sync.RWMutex
	stream           rtv1pb.Dapr_SubscribeTopicEventsServer
	publishResponses map[string]chan *rtv1pb.SubscribeTopicEventsResponse
}

func (c *conn) notifyPublishResponse(ctx context.Context, resp *rtv1pb.SubscribeTopicEventsResponse) {
	c.lock.RLock()
	ch, ok := c.publishResponses[resp.GetId()]
	c.lock.RUnlock()

	if !ok {
		log.Errorf("no client stream expecting publish response for id %q", resp.GetId())
		return
	}

	select {
	case <-ctx.Done():
	case ch <- resp:
	}
}

func (c *conn) registerPublishResponse(id string) (chan *rtv1pb.SubscribeTopicEventsResponse, func()) {
	ch := make(chan *rtv1pb.SubscribeTopicEventsResponse)
	c.lock.Lock()
	c.publishResponses[id] = ch
	c.lock.Unlock()
	return ch, func() {
		c.lock.Lock()
		delete(c.publishResponses, id)
		c.lock.Unlock()
	}
}
