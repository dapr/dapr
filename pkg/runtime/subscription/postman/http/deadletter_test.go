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

package http

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	publisherfake "github.com/dapr/dapr/pkg/runtime/pubsub/publisher/fake"
	"github.com/dapr/kit/ptr"
)

func TestSendToDeadLetter(t *testing.T) {
	newMessage := func() *contribpubsub.NewMessage {
		return &contribpubsub.NewMessage{
			Data:        []byte("test message"),
			Topic:       "orders",
			Metadata:    map[string]string{"key": "value"},
			ContentType: ptr.Of("application/json"),
		}
	}

	t.Run("publishes the message to the dead letter topic", func(t *testing.T) {
		var got *contribpubsub.PublishRequest
		h := New(Options{
			Adapter: publisherfake.New().WithPublishFn(
				func(_ context.Context, req *contribpubsub.PublishRequest) error {
					got = req
					return nil
				}),
		}).(*http)

		msg := newMessage()
		require.NoError(t, h.sendToDeadLetter(t.Context(), "testpubsub", msg, "dlq"))

		require.NotNil(t, got)
		assert.Equal(t, "dlq", got.Topic)
		assert.Equal(t, "testpubsub", got.PubsubName)
		assert.Equal(t, msg.Data, got.Data)
		assert.Equal(t, msg.Metadata, got.Metadata)
		assert.Equal(t, msg.ContentType, got.ContentType)
	})

	t.Run("returns the error when the publish fails", func(t *testing.T) {
		pubErr := errors.New("broker unavailable")
		h := New(Options{
			Adapter: publisherfake.New().WithPublishFn(
				func(_ context.Context, _ *contribpubsub.PublishRequest) error {
					return pubErr
				}),
		}).(*http)

		require.ErrorIs(t,
			h.sendToDeadLetter(t.Context(), "testpubsub", newMessage(), "dlq"),
			pubErr)
	})

	t.Run("skips the publish when the parent context was canceled", func(t *testing.T) {
		var called bool
		h := New(Options{
			Adapter: publisherfake.New().WithPublishFn(
				func(_ context.Context, _ *contribpubsub.PublishRequest) error {
					called = true
					return nil
				}),
		}).(*http)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		require.ErrorIs(t,
			h.sendToDeadLetter(ctx, "testpubsub", newMessage(), "dlq"),
			context.Canceled)
		assert.False(t, called, "publish must not be attempted on a canceled context")
	})
}
