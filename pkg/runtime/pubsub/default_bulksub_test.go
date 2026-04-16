/*
Copyright 2022 The Dapr Authors
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

package pubsub

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
)

func TestFlushMessages(t *testing.T) {
	emptyMessages := []contribPubsub.BulkMessageEntry{}
	sampleMessages := []contribPubsub.BulkMessageEntry{
		{EntryId: "1"},
		{EntryId: "2"},
	}

	sampleMsgCbMap := map[string]func(error){
		"1": func(err error) {},
		"2": func(err error) {},
	}

	t.Run("flushMessages should call handler with messages", func(t *testing.T) {
		tests := []struct {
			name                   string
			messages               []contribPubsub.BulkMessageEntry
			msgCbMap               map[string]func(error)
			expectedHandlerInvoked bool
		}{
			{
				name:                   "handler should not be invoked when messages is empty",
				messages:               emptyMessages,
				msgCbMap:               sampleMsgCbMap,
				expectedHandlerInvoked: false,
			},
			{
				name:                   "handler should be invoked with all messages when messages is not empty",
				messages:               sampleMessages,
				msgCbMap:               sampleMsgCbMap,
				expectedHandlerInvoked: true,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				handlerInvoked := false

				handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
					[]contribPubsub.BulkSubscribeResponseEntry, error,
				) {
					handlerInvoked = true

					assert.Len(t, msg.Entries, len(tc.messages))

					for _, entry := range msg.Entries {
						assert.Contains(t, tc.messages, entry)
					}

					return nil, nil
				}

				flushMessages(t.Context(), "topic", tc.messages, tc.msgCbMap, handler)
				assert.Equal(t, tc.expectedHandlerInvoked, handlerInvoked)
			})
		}
	})

	t.Run("flushMessages should invoke callbacks based on handler response", func(t *testing.T) {
		messages := []contribPubsub.BulkMessageEntry{
			{EntryId: "1"},
			{EntryId: "2"},
			{EntryId: "3"},
		}

		tests := []struct {
			name             string
			handlerResponses []contribPubsub.BulkSubscribeResponseEntry
			handlerErr       error
			entryIdErrMap    map[string]struct{} //nolint:stylecheck
		}{
			{
				"all callbacks should be invoked with nil error when handler returns nil error",
				[]contribPubsub.BulkSubscribeResponseEntry{
					{EntryId: "1"},
					{EntryId: "2"},
				},
				nil,
				map[string]struct{}{},
			},
			{
				"all callbacks should be invoked with error when handler returns error and responses is nil",
				nil,
				errors.New("handler error"),
				map[string]struct{}{
					"1": {},
					"2": {},
					"3": {},
				},
			},
			{
				"failed messages' callback should be invoked with error when handler returns error and responses is not nil",
				[]contribPubsub.BulkSubscribeResponseEntry{
					{EntryId: "1", Error: errors.New("failed message")},
					{EntryId: "2"},
					{EntryId: "3", Error: errors.New("failed message")},
				},
				errors.New("handler error"),
				map[string]struct{}{
					"1": {},
					"3": {},
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
					[]contribPubsub.BulkSubscribeResponseEntry, error,
				) {
					return tc.handlerResponses, tc.handlerErr
				}

				invokedCallbacks := make(map[string]error)

				msgCbMap := map[string]func(error){
					"1": func(err error) { invokedCallbacks["1"] = err },
					"2": func(err error) { invokedCallbacks["2"] = err },
					"3": func(err error) { invokedCallbacks["3"] = err },
				}

				flushMessages(t.Context(), "topic", messages, msgCbMap, handler)

				for id, err := range invokedCallbacks {
					if _, ok := tc.entryIdErrMap[id]; ok {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}
				}
			})
		}
	})
}

func TestProcessBulkMessagesTimerReset(t *testing.T) {
	t.Run("extra message after count-based flush should be flushed promptly", func(t *testing.T) {
		// After a count-based flush, an extra message should be flushed
		// immediately via drain-and-flush (not waiting for the timer).
		maxMessages := 3
		awaitDurationMs := 500

		cfg := contribPubsub.BulkSubscribeConfig{
			MaxMessagesCount:   maxMessages,
			MaxAwaitDurationMs: awaitDurationMs,
		}

		msgCbChan := make(chan msgWithCallback, maxMessages*2)

		var mu sync.Mutex
		flushSizes := make([]int, 0)

		handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
			[]contribPubsub.BulkSubscribeResponseEntry, error,
		) {
			mu.Lock()
			flushSizes = append(flushSizes, len(msg.Entries))
			mu.Unlock()

			return nil, nil
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go processBulkMessages(ctx, "test-topic", msgCbChan, cfg, handler)

		// Send maxMessages concurrently to trigger a count-based flush.
		var wg sync.WaitGroup

		for i := range maxMessages {
			wg.Add(1)

			go func(idx int) {
				defer wg.Done()

				done := make(chan struct{})
				msgCbChan <- msgWithCallback{
					msg: contribPubsub.BulkMessageEntry{
						EntryId: fmt.Sprintf("msg-%d", idx),
						Event:   []byte(`{}`),
					},
					cb: func(err error) {
						close(done)
					},
				}

				<-done
			}(i)
		}

		wg.Wait()

		// All maxMessages should have been flushed (possibly across
		// multiple drain-and-flush cycles due to goroutine scheduling).
		mu.Lock()
		total := 0
		for _, size := range flushSizes {
			total += size
		}
		assert.Equal(t, maxMessages, total, "all initial messages should be flushed")
		initialFlushes := len(flushSizes)
		mu.Unlock()

		// Send 1 extra message. It should be flushed immediately via
		// drain-and-flush, not waiting for the timer.
		extraDone := make(chan struct{})
		msgCbChan <- msgWithCallback{
			msg: contribPubsub.BulkMessageEntry{
				EntryId: "msg-extra",
				Event:   []byte(`{}`),
			},
			cb: func(err error) {
				close(extraDone)
			},
		}

		select {
		case <-extraDone:
			// OK — flushed promptly.
		case <-time.After(time.Duration(awaitDurationMs) * time.Millisecond):
			t.Fatal("timed out waiting for drain-and-flush of extra message")
		}

		mu.Lock()
		require.Len(t, flushSizes, initialFlushes+1, "expected one additional flush for extra message")
		assert.Equal(t, 1, flushSizes[initialFlushes], "extra flush should contain the single extra message")
		mu.Unlock()

		cancel()
	})
}

func TestProcessBulkMessagesMultipleCountFlushes(t *testing.T) {
	t.Run("two consecutive count-based flushes plus extra message", func(t *testing.T) {
		// Send 6 messages with maxMessages=3 to trigger two consecutive
		// count-based flushes, then send 1 more message and verify it is
		// flushed promptly via drain-and-flush.
		maxMessages := 3
		awaitDurationMs := 500

		cfg := contribPubsub.BulkSubscribeConfig{
			MaxMessagesCount:   maxMessages,
			MaxAwaitDurationMs: awaitDurationMs,
		}

		msgCbChan := make(chan msgWithCallback, maxMessages*3)

		var mu sync.Mutex
		flushSizes := make([]int, 0)

		handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
			[]contribPubsub.BulkSubscribeResponseEntry, error,
		) {
			mu.Lock()
			flushSizes = append(flushSizes, len(msg.Entries))
			mu.Unlock()

			return nil, nil
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go processBulkMessages(ctx, "test-topic", msgCbChan, cfg, handler)

		// Helper to send a batch of maxMessages concurrently and wait for
		// all callbacks.
		sendBatch := func(offset int) {
			var wg sync.WaitGroup

			for i := range maxMessages {
				wg.Add(1)

				go func(idx int) {
					defer wg.Done()

					done := make(chan struct{})
					msgCbChan <- msgWithCallback{
						msg: contribPubsub.BulkMessageEntry{
							EntryId: fmt.Sprintf("msg-%d", idx),
							Event:   []byte(`{}`),
						},
						cb: func(err error) {
							close(done)
						},
					}

					select {
					case <-done:
					case <-time.After(time.Duration(awaitDurationMs*2) * time.Millisecond):
						t.Errorf("timed out waiting for callback of msg-%d", idx)
					}
				}(offset + i)
			}

			wg.Wait()
		}

		// First batch.
		sendBatch(0)

		mu.Lock()
		total := 0
		for _, size := range flushSizes {
			total += size
		}
		assert.Equal(t, maxMessages, total, "all first-batch messages should be flushed")
		mu.Unlock()

		// Second batch.
		sendBatch(maxMessages)

		mu.Lock()
		total = 0
		for _, size := range flushSizes {
			total += size
		}
		assert.Equal(t, maxMessages*2, total, "all second-batch messages should be flushed")
		flushesBeforeExtra := len(flushSizes)
		mu.Unlock()

		// Send 1 extra message. It should flush promptly via drain-and-flush.
		extraDone := make(chan struct{})
		msgCbChan <- msgWithCallback{
			msg: contribPubsub.BulkMessageEntry{
				EntryId: "msg-extra",
				Event:   []byte(`{}`),
			},
			cb: func(err error) {
				close(extraDone)
			},
		}

		select {
		case <-extraDone:
			// OK — flushed promptly.
		case <-time.After(time.Duration(awaitDurationMs) * time.Millisecond):
			t.Fatal("timed out waiting for drain-and-flush of extra message")
		}

		mu.Lock()
		require.Len(t, flushSizes, flushesBeforeExtra+1, "expected one additional flush for extra message")
		assert.Equal(t, 1, flushSizes[flushesBeforeExtra], "extra flush should contain the single extra message")
		mu.Unlock()

		cancel()
	})
}

func TestProcessBulkMessagesPartialBatchFlush(t *testing.T) {
	t.Run("messages fewer than maxMessages should flush promptly via drain-and-flush", func(t *testing.T) {
		// Send fewer messages than maxMessagesCount and verify they are
		// flushed promptly via drain-and-flush (not waiting for the timer).
		maxMessages := 5
		awaitDurationMs := 5000 // Long timer to prove we don't depend on it.

		cfg := contribPubsub.BulkSubscribeConfig{
			MaxMessagesCount:   maxMessages,
			MaxAwaitDurationMs: awaitDurationMs,
		}

		msgCbChan := make(chan msgWithCallback, maxMessages)

		var mu sync.Mutex
		var flushSize int

		flushed := make(chan struct{})
		flushOnce := sync.Once{}

		handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
			[]contribPubsub.BulkSubscribeResponseEntry, error,
		) {
			mu.Lock()
			flushSize = len(msg.Entries)
			mu.Unlock()
			flushOnce.Do(func() { close(flushed) })

			return nil, nil
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// Enqueue both messages BEFORE starting processBulkMessages so
		// they are guaranteed to be in the channel when it reads.
		for i := range 2 {
			msgCbChan <- msgWithCallback{
				msg: contribPubsub.BulkMessageEntry{
					EntryId: fmt.Sprintf("msg-%d", i),
					Event:   []byte(`{}`),
				},
				cb: func(err error) {},
			}
		}

		go processBulkMessages(ctx, "test-topic", msgCbChan, cfg, handler)

		// The messages should be flushed promptly via drain-and-flush.
		select {
		case <-flushed:
			// OK
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timed out waiting for drain-and-flush; messages should flush immediately")
		}

		mu.Lock()
		assert.Equal(t, 2, flushSize, "all buffered messages should be flushed together")
		mu.Unlock()

		cancel()
	})
}

func TestProcessBulkMessagesSyncMode(t *testing.T) {
	t.Run("sync-mode serial delivery should flush each message promptly", func(t *testing.T) {
		// Simulates a sync-mode component: messages are delivered
		// one-at-a-time, each blocking until the handler returns. Without
		// drain-and-flush, each message would wait for MaxAwaitDurationMs.
		maxMessages := 10
		awaitDurationMs := 5000 // Long timer to prove we don't depend on it.
		numMessages := 5

		cfg := contribPubsub.BulkSubscribeConfig{
			MaxMessagesCount:   maxMessages,
			MaxAwaitDurationMs: awaitDurationMs,
		}

		msgCbChan := make(chan msgWithCallback, maxMessages)

		var mu sync.Mutex
		flushSizes := make([]int, 0)

		handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
			[]contribPubsub.BulkSubscribeResponseEntry, error,
		) {
			mu.Lock()
			flushSizes = append(flushSizes, len(msg.Entries))
			mu.Unlock()

			return nil, nil
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go processBulkMessages(ctx, "test-topic", msgCbChan, cfg, handler)

		// Send messages serially, waiting for each callback before sending
		// the next (simulating sync-mode component behavior).
		start := time.Now()

		for i := range numMessages {
			done := make(chan struct{})
			msgCbChan <- msgWithCallback{
				msg: contribPubsub.BulkMessageEntry{
					EntryId: fmt.Sprintf("msg-%d", i),
					Event:   []byte(`{}`),
				},
				cb: func(err error) {
					close(done)
				},
			}

			select {
			case <-done:
			case <-time.After(time.Duration(awaitDurationMs) * time.Millisecond):
				t.Fatalf("timed out waiting for callback of msg-%d", i)
			}
		}

		elapsed := time.Since(start)

		// All 5 messages should complete well under awaitDurationMs.
		// Without the fix this would take 5 * 5000ms = 25s.
		maxExpectedElapsed := time.Duration(awaitDurationMs/2) * time.Millisecond
		assert.Less(t, elapsed, maxExpectedElapsed,
			"sync-mode messages should be flushed promptly, not waiting for timer")

		mu.Lock()
		// Each message should be flushed individually (batch of 1) since
		// the channel is empty when each message arrives.
		require.Len(t, flushSizes, numMessages, "expected one flush per message in sync mode")
		for i, size := range flushSizes {
			assert.Equal(t, 1, size, "flush %d should contain exactly 1 message", i)
		}
		mu.Unlock()

		cancel()
	})
}

func TestProcessBulkMessagesAsyncBatching(t *testing.T) {
	t.Run("concurrent messages should be batched efficiently", func(t *testing.T) {
		// Simulates an async-mode component: multiple messages are queued
		// concurrently. The drain-and-flush should collect them into a
		// single batch.
		maxMessages := 10
		awaitDurationMs := 5000
		numMessages := 5

		cfg := contribPubsub.BulkSubscribeConfig{
			MaxMessagesCount:   maxMessages,
			MaxAwaitDurationMs: awaitDurationMs,
		}

		msgCbChan := make(chan msgWithCallback, maxMessages)

		var mu sync.Mutex
		flushSizes := make([]int, 0)

		handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
			[]contribPubsub.BulkSubscribeResponseEntry, error,
		) {
			mu.Lock()
			flushSizes = append(flushSizes, len(msg.Entries))
			mu.Unlock()

			return nil, nil
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go processBulkMessages(ctx, "test-topic", msgCbChan, cfg, handler)

		// Send all messages concurrently (simulating async-mode component).
		var wg sync.WaitGroup

		for i := range numMessages {
			wg.Add(1)

			go func(idx int) {
				defer wg.Done()

				done := make(chan struct{})
				msgCbChan <- msgWithCallback{
					msg: contribPubsub.BulkMessageEntry{
						EntryId: fmt.Sprintf("msg-%d", idx),
						Event:   []byte(`{}`),
					},
					cb: func(err error) {
						close(done)
					},
				}

				select {
				case <-done:
				case <-time.After(time.Duration(awaitDurationMs) * time.Millisecond):
					t.Errorf("timed out waiting for callback of msg-%d", idx)
				}
			}(i)
		}

		wg.Wait()

		mu.Lock()
		// All messages should have been flushed. The total across all
		// flushes should equal numMessages.
		total := 0
		for _, size := range flushSizes {
			total += size
		}
		assert.Equal(t, numMessages, total, "all messages should be flushed")
		// Batching depends on goroutine scheduling; log for observability.
		if len(flushSizes) < numMessages {
			t.Logf("batching observed: %d messages in %d flushes", numMessages, len(flushSizes))
		}
		mu.Unlock()

		cancel()
	})
}
