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
	t.Run("timer should reset after maxMessagesCount flush", func(t *testing.T) {
		// This test verifies that after a batch is flushed because
		// maxMessagesCount was reached, the await duration timer is reset.
		// Without the reset, the timer would fire prematurely for the next
		// batch.
		//
		// Strategy: use maxMessages=3 and awaitDuration=500ms. Send 3
		// messages concurrently to trigger a count-based flush. Then
		// immediately send 1 more message. If the timer is properly reset,
		// that extra message should NOT be flushed until ~500ms after the
		// count-based flush. We check at 250ms that it hasn't been flushed.

		maxMessages := 3
		awaitDurationMs := 500

		cfg := contribPubsub.BulkSubscribeConfig{
			MaxMessagesCount:   maxMessages,
			MaxAwaitDurationMs: awaitDurationMs,
		}

		msgCbChan := make(chan msgWithCallback, maxMessages*2)

		var mu sync.Mutex
		flushTimes := make([]time.Time, 0) // records the time of each flush
		flushSizes := make([]int, 0)

		handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
			[]contribPubsub.BulkSubscribeResponseEntry, error,
		) {
			mu.Lock()
			flushTimes = append(flushTimes, time.Now())
			flushSizes = append(flushSizes, len(msg.Entries))
			mu.Unlock()
			return nil, nil
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go processBulkMessages(ctx, "test-topic", msgCbChan, cfg, handler)

		// Send maxMessages concurrently so they all buffer before any
		// callback blocks the sender.
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

		// First flush happened due to maxMessagesCount. Record the time.
		mu.Lock()
		require.Len(t, flushSizes, 1, "expected exactly 1 flush after sending maxMessages")
		mu.Unlock()

		// Now send 1 extra message immediately after the count-based flush.
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

		// Wait 250ms (half of awaitDuration). The extra message should NOT
		// have been flushed yet if the timer was properly reset.
		time.Sleep(250 * time.Millisecond)

		mu.Lock()
		assert.Len(t, flushSizes, 1,
			"expected still only 1 flush 250ms after count-based flush (timer should have been reset)")
		mu.Unlock()

		// Wait for the extra message to be flushed by the timer, with a
		// timeout to avoid hanging indefinitely if the fix is broken.
		select {
		case <-extraDone:
			// OK
		case <-time.After(time.Duration(awaitDurationMs*2) * time.Millisecond):
			t.Fatal("timed out waiting for timer-based flush of extra message; timer was likely not reset after count-based flush")
		}

		mu.Lock()
		require.Len(t, flushSizes, 2, "expected 2 flushes total")
		assert.Equal(t, maxMessages, flushSizes[0])
		assert.Equal(t, 1, flushSizes[1], "second flush should contain the single extra message")
		// Verify the second flush happened roughly awaitDuration after the first.
		elapsed := flushTimes[1].Sub(flushTimes[0])
		assert.Greater(t, elapsed, time.Duration(awaitDurationMs*40/100)*time.Millisecond,
			"second flush should happen after a significant portion of awaitDuration")
		mu.Unlock()

		cancel()
	})
}

func TestProcessBulkMessagesMultipleCountFlushes(t *testing.T) {
	t.Run("two consecutive count-based flushes should each reset the timer", func(t *testing.T) {
		// Send 6 messages with maxMessages=3 to trigger two consecutive
		// count-based flushes, then send 1 more message and verify that
		// it is NOT flushed prematurely (timer was reset after each flush).

		maxMessages := 3
		awaitDurationMs := 500

		cfg := contribPubsub.BulkSubscribeConfig{
			MaxMessagesCount:   maxMessages,
			MaxAwaitDurationMs: awaitDurationMs,
		}

		msgCbChan := make(chan msgWithCallback, maxMessages*3)

		var mu sync.Mutex
		flushTimes := make([]time.Time, 0)
		flushSizes := make([]int, 0)

		handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
			[]contribPubsub.BulkSubscribeResponseEntry, error,
		) {
			mu.Lock()
			flushTimes = append(flushTimes, time.Now())
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

		// First count-based flush.
		sendBatch(0)
		mu.Lock()
		require.Len(t, flushSizes, 1, "expected 1 flush after first batch")
		assert.Equal(t, maxMessages, flushSizes[0])
		mu.Unlock()

		// Second count-based flush.
		sendBatch(maxMessages)
		mu.Lock()
		require.Len(t, flushSizes, 2, "expected 2 flushes after second batch")
		assert.Equal(t, maxMessages, flushSizes[1])
		mu.Unlock()

		// Now send 1 extra message. It should NOT flush for ~awaitDuration.
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

		// At 250ms the extra message should still be buffered.
		time.Sleep(250 * time.Millisecond)
		mu.Lock()
		assert.Len(t, flushSizes, 2,
			"expected still only 2 flushes 250ms after second count-based flush")
		mu.Unlock()

		// Wait for the timer-based flush with a safety timeout.
		select {
		case <-extraDone:
		case <-time.After(time.Duration(awaitDurationMs*2) * time.Millisecond):
			t.Fatal("timed out waiting for timer-based flush of extra message after two consecutive count-based flushes")
		}

		mu.Lock()
		require.Len(t, flushSizes, 3, "expected 3 flushes total")
		assert.Equal(t, 1, flushSizes[2], "third flush should contain the single extra message")
		elapsed := flushTimes[2].Sub(flushTimes[1])
		assert.Greater(t, elapsed, time.Duration(awaitDurationMs*40/100)*time.Millisecond,
			"third flush should happen after a significant portion of awaitDuration from second flush")
		mu.Unlock()

		cancel()
	})
}

func TestProcessBulkMessagesPureTimerFlush(t *testing.T) {
	t.Run("messages fewer than maxMessages should flush after awaitDuration", func(t *testing.T) {
		// Baseline test: send fewer messages than maxMessagesCount and
		// verify they are flushed by the timer (not by a count trigger).

		maxMessages := 5
		awaitDurationMs := 300

		cfg := contribPubsub.BulkSubscribeConfig{
			MaxMessagesCount:   maxMessages,
			MaxAwaitDurationMs: awaitDurationMs,
		}

		msgCbChan := make(chan msgWithCallback, maxMessages)

		var mu sync.Mutex
		var flushTime time.Time
		var flushSize int
		flushed := make(chan struct{})
		flushOnce := sync.Once{}

		handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
			[]contribPubsub.BulkSubscribeResponseEntry, error,
		) {
			mu.Lock()
			flushTime = time.Now()
			flushSize = len(msg.Entries)
			mu.Unlock()
			flushOnce.Do(func() { close(flushed) })
			return nil, nil
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go processBulkMessages(ctx, "test-topic", msgCbChan, cfg, handler)

		// Send 2 messages (less than maxMessages=5).
		sendTime := time.Now()
		for i := range 2 {
			msgCbChan <- msgWithCallback{
				msg: contribPubsub.BulkMessageEntry{
					EntryId: fmt.Sprintf("msg-%d", i),
					Event:   []byte(`{}`),
				},
				cb: func(err error) {},
			}
		}

		// The messages should NOT have been flushed yet (no count trigger).
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		assert.True(t, flushTime.IsZero(),
			"messages should not be flushed before awaitDuration elapses")
		mu.Unlock()

		// Wait for the timer to fire, with a safety timeout.
		select {
		case <-flushed:
		case <-time.After(time.Duration(awaitDurationMs*3) * time.Millisecond):
			t.Fatal("timed out waiting for timer-based flush; awaitDuration may not be triggering correctly")
		}

		mu.Lock()
		assert.Equal(t, 2, flushSize, "all buffered messages should be flushed together")
		elapsed := flushTime.Sub(sendTime)
		assert.Greater(t, elapsed, time.Duration(awaitDurationMs*80/100)*time.Millisecond,
			"flush should happen close to awaitDuration after messages were sent")
		assert.Less(t, elapsed, time.Duration(awaitDurationMs*200/100)*time.Millisecond,
			"flush should not take excessively longer than awaitDuration")
		mu.Unlock()

		cancel()
	})
}
