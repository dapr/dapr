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

package streamer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func TestNotifyPublishResponse(t *testing.T) {
	newConn := func() *conn {
		return &conn{connectionID: 1, publishResponses: make(PublishResponses)}
	}
	resp := &rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1{Id: "a"}

	t.Run("a duplicate response does not block", func(t *testing.T) {
		c := newConn()
		ch, cleanup := c.registerPublishResponse("a")
		defer cleanup()

		done := make(chan struct{})
		go func() {
			c.notifyPublishResponse(resp)
			c.notifyPublishResponse(resp)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			require.Fail(t, "a second response for the same id blocked the receive loop")
		}
		assert.Equal(t, "a", (<-ch).GetId())
	})

	t.Run("cleanup leaves the channel open for a receive loop that still holds it", func(t *testing.T) {
		c := newConn()
		ch, cleanup := c.registerPublishResponse("a")
		cleanup()
		assert.NotPanics(t, func() {
			select {
			case ch <- resp:
			default:
			}
		})
	})
}
