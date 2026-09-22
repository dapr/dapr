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
	"github.com/dapr/dapr/pkg/runtime/subscription/todo"
)

func TestSendBulkToDeadLetter(t *testing.T) {
	// entries 1 and 3 failed delivery to the app; entry 2 succeeded.
	newFixture := func(retryReported bool) (*todo.BulkSubscribeCallData, *contribpubsub.BulkMessage, *todo.BulkSubIngressDiagnostics) {
		entries := []contribpubsub.BulkMessageEntry{
			{EntryId: "1", Event: []byte("one")},
			{EntryId: "2", Event: []byte("two")},
			{EntryId: "3", Event: []byte("three")},
		}
		responses := []contribpubsub.BulkSubscribeResponseEntry{
			{EntryId: "1", Error: errors.New("app failed")},
			{EntryId: "2"},
			{EntryId: "3", Error: errors.New("app failed")},
		}
		indexMap := map[string]int{"1": 0, "2": 1, "3": 2}
		diag := &todo.BulkSubIngressDiagnostics{
			StatusWiseDiag: map[string]int64{string(contribpubsub.Retry): 3},
			RetryReported:  retryReported,
		}
		callData := &todo.BulkSubscribeCallData{
			BulkResponses:   &responses,
			BulkSubDiag:     diag,
			EntryIdIndexMap: &indexMap,
			PsName:          "testpubsub",
			Topic:           "orders",
		}
		msg := &contribpubsub.BulkMessage{
			Entries:  entries,
			Topic:    "orders",
			Metadata: map[string]string{"key": "value"},
		}
		return callData, msg, diag
	}

	t.Run("sends all entries when sendAllEntries is true", func(t *testing.T) {
		var got *contribpubsub.BulkPublishRequest
		h := New(Options{
			Adapter: publisherfake.New().WithBulkPublishFn(
				func(_ context.Context, req *contribpubsub.BulkPublishRequest) (contribpubsub.BulkPublishResponse, error) {
					got = req
					return contribpubsub.BulkPublishResponse{}, nil
				}),
		}).(*http)

		callData, msg, diag := newFixture(false)
		require.NoError(t, h.sendBulkToDeadLetter(t.Context(), callData, msg, "dlq", true))

		require.NotNil(t, got)
		assert.Equal(t, "dlq", got.Topic)
		assert.Equal(t, "testpubsub", got.PubsubName)
		assert.Equal(t, msg.Metadata, got.Metadata)
		assert.Len(t, got.Entries, 3)
		assert.Equal(t, int64(3), diag.StatusWiseDiag[string(contribpubsub.Drop)])
		assert.Equal(t, int64(3), diag.StatusWiseDiag[string(contribpubsub.Retry)])
	})

	t.Run("sends only failed entries and moves them from retry to drop", func(t *testing.T) {
		var got *contribpubsub.BulkPublishRequest
		h := New(Options{
			Adapter: publisherfake.New().WithBulkPublishFn(
				func(_ context.Context, req *contribpubsub.BulkPublishRequest) (contribpubsub.BulkPublishResponse, error) {
					got = req
					return contribpubsub.BulkPublishResponse{}, nil
				}),
		}).(*http)

		callData, msg, diag := newFixture(true)
		require.NoError(t, h.sendBulkToDeadLetter(t.Context(), callData, msg, "dlq", false))

		require.NotNil(t, got)
		require.Len(t, got.Entries, 2)
		assert.Equal(t, "1", got.Entries[0].EntryId)
		assert.Equal(t, "3", got.Entries[1].EntryId)
		assert.Equal(t, int64(2), diag.StatusWiseDiag[string(contribpubsub.Drop)])
		assert.Equal(t, int64(1), diag.StatusWiseDiag[string(contribpubsub.Retry)])
	})

	t.Run("returns the error when the bulk publish fails", func(t *testing.T) {
		pubErr := errors.New("broker unavailable")
		h := New(Options{
			Adapter: publisherfake.New().WithBulkPublishFn(
				func(_ context.Context, _ *contribpubsub.BulkPublishRequest) (contribpubsub.BulkPublishResponse, error) {
					return contribpubsub.BulkPublishResponse{}, pubErr
				}),
		}).(*http)

		callData, msg, _ := newFixture(false)
		require.ErrorIs(t,
			h.sendBulkToDeadLetter(t.Context(), callData, msg, "dlq", true),
			pubErr)
	})

	t.Run("skips the publish when the parent context was canceled", func(t *testing.T) {
		var called bool
		h := New(Options{
			Adapter: publisherfake.New().WithBulkPublishFn(
				func(_ context.Context, _ *contribpubsub.BulkPublishRequest) (contribpubsub.BulkPublishResponse, error) {
					called = true
					return contribpubsub.BulkPublishResponse{}, nil
				}),
		}).(*http)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		callData, msg, _ := newFixture(false)
		require.ErrorIs(t,
			h.sendBulkToDeadLetter(ctx, callData, msg, "dlq", true),
			context.Canceled)
		assert.False(t, called, "bulk publish must not be attempted on a canceled context")
	})
}
