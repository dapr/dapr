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
	"sync/atomic"
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

// TestProcessBulkMessagesCountFlushResetsTicker verifies the dapr/dapr#9211
// fix: after a count-based flush, a single leftover message must wait a full
// MaxAwaitDurationMs window (measured from the flush) before being delivered,
// not the stale remainder of the original tick that started when the
// subscription was created.
func TestProcessBulkMessagesCountFlushResetsTicker(t *testing.T) {
	maxMessages := 3
	awaitDurationMs := 400

	cfg := contribPubsub.BulkSubscribeConfig{
		MaxMessagesCount:   maxMessages,
		MaxAwaitDurationMs: awaitDurationMs,
	}

	msgCbChan := make(chan msgWithCallback, maxMessages*2)

	type flushRecord struct {
		at   time.Time
		size int
	}

	var mu sync.Mutex
	flushes := make([]flushRecord, 0, 4)

	handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
		[]contribPubsub.BulkSubscribeResponseEntry, error,
	) {
		mu.Lock()
		flushes = append(flushes, flushRecord{at: time.Now(), size: len(msg.Entries)})
		mu.Unlock()
		return nil, nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go processBulkMessagesBatch(ctx, "test-topic", msgCbChan, cfg, handler)

	// Publish maxMessages concurrently so we hit the count threshold in
	// one wake-up.
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
				cb: func(error) { close(done) },
			}
			<-done
		}(i)
	}
	wg.Wait()

	mu.Lock()
	require.Len(t, flushes, 1, "expected exactly one count-based flush")
	assert.Equal(t, maxMessages, flushes[0].size, "first flush should contain all maxMessages")
	firstFlush := flushes[0].at
	mu.Unlock()

	// Send a single extra message. It must NOT be flushed immediately and
	// must NOT be flushed at the stale original tick. It must be flushed
	// at first_flush + awaitDuration ± tolerance.
	extraSent := time.Now()
	extraDone := make(chan struct{})
	msgCbChan <- msgWithCallback{
		msg: contribPubsub.BulkMessageEntry{
			EntryId: "msg-extra",
			Event:   []byte(`{}`),
		},
		cb: func(error) { close(extraDone) },
	}

	select {
	case <-extraDone:
	case <-time.After(time.Duration(awaitDurationMs*3) * time.Millisecond):
		t.Fatal("timed out waiting for timer-based flush of leftover message")
	}

	mu.Lock()
	require.Len(t, flushes, 2, "expected exactly two flushes total")
	assert.Equal(t, 1, flushes[1].size, "second flush should contain the leftover message")
	secondFlush := flushes[1].at
	mu.Unlock()

	elapsedFromFirstFlush := secondFlush.Sub(firstFlush)
	elapsedFromExtraSent := secondFlush.Sub(extraSent)

	// Lower bound: the timer was reset at firstFlush, so the second flush
	// must happen at least close to awaitDuration after firstFlush. Use a
	// loose lower bound (80% of awaitDuration) to tolerate scheduling
	// jitter while still proving the timer was reset.
	lowerBound := time.Duration(awaitDurationMs) * time.Millisecond * 8 / 10
	require.GreaterOrEqual(t, elapsedFromFirstFlush, lowerBound,
		"leftover must wait ~awaitDuration after count-based flush (timer was reset); got %v", elapsedFromFirstFlush)

	// Upper bound: the leftover should flush within a reasonable window of
	// being sent; prevents the test from passing trivially on a stalled loop.
	upperBound := time.Duration(awaitDurationMs*2) * time.Millisecond
	require.Less(t, elapsedFromExtraSent, upperBound,
		"leftover should flush within 2x awaitDuration of arrival; got %v", elapsedFromExtraSent)
}

// TestProcessBulkMessagesLeftoverJoinsNextBatch reproduces the customer's
// 12+12 scenario: two bursts that each exceed MaxMessagesCount leave a
// residual tail; the tail must accumulate across the bursts and flush
// exactly once at the timer expiration that follows the last count-based
// flush (dapr/dapr#9211).
func TestProcessBulkMessagesLeftoverJoinsNextBatch(t *testing.T) {
	maxMessages := 10
	awaitDurationMs := 400
	burstSize := 12

	cfg := contribPubsub.BulkSubscribeConfig{
		MaxMessagesCount:   maxMessages,
		MaxAwaitDurationMs: awaitDurationMs,
	}

	msgCbChan := make(chan msgWithCallback, burstSize*2)

	type flushRecord struct {
		at   time.Time
		size int
	}

	var mu sync.Mutex
	flushes := make([]flushRecord, 0, 4)

	handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
		[]contribPubsub.BulkSubscribeResponseEntry, error,
	) {
		mu.Lock()
		flushes = append(flushes, flushRecord{at: time.Now(), size: len(msg.Entries)})
		mu.Unlock()
		return nil, nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go processBulkMessagesBatch(ctx, "test-topic", msgCbChan, cfg, handler)

	// fireBurst sends burstSize messages asynchronously without waiting
	// for their callbacks. Senders are background goroutines; the cancel
	// defer above cleans them up.
	fireBurst := func(offset int) {
		for i := range burstSize {
			go func(idx int) {
				done := make(chan struct{})
				msgCbChan <- msgWithCallback{
					msg: contribPubsub.BulkMessageEntry{
						EntryId: fmt.Sprintf("msg-%d", offset+idx),
						Event:   []byte(`{}`),
					},
					cb: func(error) { close(done) },
				}
				<-done
			}(i)
		}
	}

	fireBurst(0)

	// Wait for the first count-based flush of maxMessages.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		mu.Lock()
		defer mu.Unlock()
		require.Len(c, flushes, 1, "expected exactly one flush so far")
		require.Equal(c, maxMessages, flushes[0].size, "first flush should be count-based")
	}, time.Duration(awaitDurationMs)*time.Millisecond, 5*time.Millisecond)

	// Sleep less than awaitDuration so the second burst arrives before
	// the timer would fire for the trailing 2 messages of burst 1.
	sleep := time.Duration(awaitDurationMs*3/10) * time.Millisecond
	time.Sleep(sleep)

	// At this point the trailing 2 of burst 1 must still be buffered
	// (no timer-based flush yet, no drain-and-flush).
	mu.Lock()
	require.Len(t, flushes, 1, "no second flush expected before second burst")
	mu.Unlock()

	fireBurst(burstSize)

	// Wait for the second count-based flush.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		mu.Lock()
		defer mu.Unlock()
		require.Len(c, flushes, 2, "expected exactly two flushes after second burst")
		require.Equal(c, maxMessages, flushes[1].size, "second flush should be count-based")
	}, time.Duration(awaitDurationMs)*time.Millisecond, 5*time.Millisecond)

	mu.Lock()
	secondFlush := flushes[1].at
	mu.Unlock()

	// The 4 remaining messages (2 trailing from burst 1 + 2 trailing from
	// burst 2) must now wait awaitDuration from the second flush, NOT
	// fire immediately and NOT fire on the stale original tick.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		mu.Lock()
		defer mu.Unlock()
		require.Len(c, flushes, 3, "expected exactly three flushes after leftover")
		require.Equal(c, burstSize*2-maxMessages*2, flushes[2].size,
			"third flush should contain the 4 leftover messages")
	}, time.Duration(awaitDurationMs*3)*time.Millisecond, 5*time.Millisecond)

	mu.Lock()
	thirdFlush := flushes[2].at
	mu.Unlock()

	elapsed := thirdFlush.Sub(secondFlush)
	lowerBound := time.Duration(awaitDurationMs) * time.Millisecond * 8 / 10
	upperBound := time.Duration(awaitDurationMs) * time.Millisecond * 15 / 10

	require.GreaterOrEqual(t, elapsed, lowerBound,
		"leftover flush must be >= ~awaitDuration after second count flush; got %v", elapsed)
	require.LessOrEqual(t, elapsed, upperBound,
		"leftover flush must be <= ~1.5x awaitDuration after second count flush; got %v", elapsed)
}

// TestProcessBulkMessagesPartialBatchTimerFlush verifies that a partial
// batch (fewer than MaxMessagesCount) is not flushed until the timer
// fires.
func TestProcessBulkMessagesPartialBatchTimerFlush(t *testing.T) {
	maxMessages := 10
	awaitDurationMs := 300

	cfg := contribPubsub.BulkSubscribeConfig{
		MaxMessagesCount:   maxMessages,
		MaxAwaitDurationMs: awaitDurationMs,
	}

	msgCbChan := make(chan msgWithCallback, maxMessages)

	var flushSize atomic.Int32
	flushed := make(chan struct{})

	handler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
		[]contribPubsub.BulkSubscribeResponseEntry, error,
	) {
		flushSize.Store(int32(len(msg.Entries))) //nolint:gosec
		close(flushed)
		return nil, nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go processBulkMessagesBatch(ctx, "test-topic", msgCbChan, cfg, handler)

	const numMessages = 2
	firstSent := time.Now()
	for i := range numMessages {
		msgCbChan <- msgWithCallback{
			msg: contribPubsub.BulkMessageEntry{
				EntryId: fmt.Sprintf("msg-%d", i),
				Event:   []byte(`{}`),
			},
			cb: func(error) {},
		}
	}

	// The partial batch must NOT be flushed before ~awaitDuration has
	// elapsed.
	minWait := time.Duration(awaitDurationMs) * time.Millisecond * 7 / 10
	select {
	case <-flushed:
		elapsed := time.Since(firstSent)
		if elapsed < minWait {
			t.Fatalf("partial batch flushed too early: %v < %v (no drain-and-flush allowed)", elapsed, minWait)
		}
	case <-time.After(time.Duration(awaitDurationMs*3) * time.Millisecond):
		t.Fatal("partial batch never flushed")
	}

	assert.Equal(t, int32(numMessages), flushSize.Load(),
		"partial batch should contain all %d buffered messages", numMessages)
}

// TestProcessBulkMessagesAsyncBurstBatching verifies the drain-in-select
// optimization: concurrent arrivals share a single wake-up and are
// delivered in one batch.
func TestProcessBulkMessagesAsyncBurstBatching(t *testing.T) {
	maxMessages := 10
	awaitDurationMs := 500
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

	go processBulkMessagesBatch(ctx, "test-topic", msgCbChan, cfg, handler)

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
				cb: func(error) { close(done) },
			}
			<-done
		}(i)
	}
	wg.Wait()

	mu.Lock()
	total := 0
	for _, size := range flushSizes {
		total += size
	}
	assert.Equal(t, numMessages, total, "all messages should be flushed")
	mu.Unlock()
}

// TestProcessBulkMessagesMultipleCountFlushes verifies that two
// back-to-back bursts that each hit MaxMessagesCount produce two
// count-based flushes, each of size MaxMessagesCount.
func TestProcessBulkMessagesMultipleCountFlushes(t *testing.T) {
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

	go processBulkMessagesBatch(ctx, "test-topic", msgCbChan, cfg, handler)

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
					cb: func(error) { close(done) },
				}
				<-done
			}(offset + i)
		}
		wg.Wait()
	}

	sendBatch(0)
	sendBatch(maxMessages)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, flushSizes, 2, "expected two count-based flushes")
	assert.Equal(t, maxMessages, flushSizes[0])
	assert.Equal(t, maxMessages, flushSizes[1])
}

// TestProcessBulkMessagesImmediateFlushesOnArrival verifies that the
// immediate path delivers each message as it arrives without waiting
// for a timer or a count threshold. This is the path that components
// declaring pubsub.FeatureBulkSubscribeImmediate (e.g. Pulsar with
// processMode=sync) take to avoid piling up unacked messages.
func TestProcessBulkMessagesImmediateFlushesOnArrival(t *testing.T) {
	maxMessages := 100
	// Long await so the test would block forever if a timer were still
	// in play.
	awaitDurationMs := 5000

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

	go processBulkMessagesImmediate(ctx, "test-topic", msgCbChan, cfg, handler)

	const numMessages = 3
	start := time.Now()
	for i := range numMessages {
		done := make(chan struct{})
		msgCbChan <- msgWithCallback{
			msg: contribPubsub.BulkMessageEntry{
				EntryId: fmt.Sprintf("msg-%d", i),
				Event:   []byte(`{}`),
			},
			cb: func(error) { close(done) },
		}
		select {
		case <-done:
		case <-time.After(time.Duration(awaitDurationMs/2) * time.Millisecond):
			t.Fatalf("sync flush should ack msg-%d well under awaitDuration", i)
		}
	}

	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, flushSizes, numMessages, "expected one flush per message")
	for i, size := range flushSizes {
		require.Equal(t, 1, size, "flush %d should carry one message (no batching window in sync mode)", i)
	}
	// Sanity cap: 3 messages should complete in well under the await
	// duration. Without the sync path they would take ≥ 3 ×
	// awaitDuration.
	require.Less(t, elapsed, time.Duration(awaitDurationMs/2)*time.Millisecond,
		"sync mode should not wait for the timer; got %v", elapsed)
}

// TestProcessBulkMessagesSyncBurstCoalesce verifies that when multiple
// messages happen to land in the channel concurrently (for example,
// two Pulsar partitions delivering at once), the sync path still
// coalesces them via drainChannel up to MaxMessagesCount instead of
// producing N one-entry flushes. This keeps the path well-behaved
// under any residual concurrency a sync-mode component might exhibit.
func TestProcessBulkMessagesSyncBurstCoalesce(t *testing.T) {
	maxMessages := 10
	awaitDurationMs := 5000

	cfg := contribPubsub.BulkSubscribeConfig{
		MaxMessagesCount:   maxMessages,
		MaxAwaitDurationMs: awaitDurationMs,
	}

	msgCbChan := make(chan msgWithCallback, maxMessages)

	var mu sync.Mutex
	flushSizes := make([]int, 0)

	// Pre-fill the channel so processBulkMessagesSync wakes up on
	// the first message and drainChannel sees the rest already queued.
	const numMessages = 5
	for i := range numMessages {
		msgCbChan <- msgWithCallback{
			msg: contribPubsub.BulkMessageEntry{
				EntryId: fmt.Sprintf("msg-%d", i),
				Event:   []byte(`{}`),
			},
			cb: func(error) {},
		}
	}

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

	go processBulkMessagesImmediate(ctx, "test-topic", msgCbChan, cfg, handler)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		mu.Lock()
		defer mu.Unlock()
		total := 0
		for _, s := range flushSizes {
			total += s
		}
		require.GreaterOrEqual(c, total, numMessages, "all messages should flush")
	}, 500*time.Millisecond, 5*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, flushSizes, 1,
		"drainChannel should coalesce the 5 pre-queued messages into one flush")
	require.Equal(t, numMessages, flushSizes[0],
		"coalesced flush should carry all pre-queued messages")
}

// fakeFeaturePubSub is a minimal contribPubsub.PubSub used to drive
// BulkSubscribe through its feature-based dispatch and to inject a
// single message via the captured subscriber callback.
type fakeFeaturePubSub struct {
	features []contribPubsub.Feature
	handler  contribPubsub.Handler
	ready    chan struct{}
}

func (f *fakeFeaturePubSub) Init(context.Context, contribPubsub.Metadata) error { return nil }
func (f *fakeFeaturePubSub) Features() []contribPubsub.Feature                  { return f.features }
func (f *fakeFeaturePubSub) Publish(context.Context, *contribPubsub.PublishRequest) error {
	return nil
}

func (f *fakeFeaturePubSub) Subscribe(ctx context.Context, _ contribPubsub.SubscribeRequest, handler contribPubsub.Handler) error {
	f.handler = handler
	close(f.ready)
	<-ctx.Done()
	return nil
}

func (f *fakeFeaturePubSub) Close() error { return nil }

// TestBulkSubscribeFeatureDispatchImmediate verifies that when the
// upstream component declares FeatureBulkSubscribeImmediate,
// BulkSubscribe routes a single arrival through the immediate path —
// the handler returns within milliseconds rather than waiting for
// MaxAwaitDurationMs.
func TestBulkSubscribeFeatureDispatchImmediate(t *testing.T) {
	fake := &fakeFeaturePubSub{
		features: []contribPubsub.Feature{contribPubsub.FeatureBulkSubscribeImmediate},
		ready:    make(chan struct{}),
	}

	sub := NewDefaultBulkSubscriber(fake)

	flushed := make(chan struct{})
	bulkHandler := func(_ context.Context, msg *contribPubsub.BulkMessage) ([]contribPubsub.BulkSubscribeResponseEntry, error) {
		require.Len(t, msg.Entries, 1)
		close(flushed)
		return nil, nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	subErr := make(chan error, 1)
	go func() {
		subErr <- sub.BulkSubscribe(ctx, contribPubsub.SubscribeRequest{
			Topic: "test-topic",
			BulkSubscribeConfig: contribPubsub.BulkSubscribeConfig{
				MaxMessagesCount:   100,
				MaxAwaitDurationMs: 5_000, // long enough that batch path would block the test
			},
		}, bulkHandler)
	}()

	<-fake.ready

	handlerErr := make(chan error, 1)
	go func() {
		handlerErr <- fake.handler(ctx, &contribPubsub.NewMessage{
			Data:  []byte(`{}`),
			Topic: "test-topic",
		})
	}()

	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("immediate path should flush within 1s; got stuck waiting for the timer")
	}

	select {
	case err := <-handlerErr:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("subscriber callback should return after the immediate flush ack")
	}

	cancel()
	<-subErr
}

// TestBulkSubscribeFeatureDispatchBatch verifies that when the
// component does NOT declare FeatureBulkSubscribeImmediate, a single
// arrival is held by the batch path until MaxAwaitDurationMs elapses.
func TestBulkSubscribeFeatureDispatchBatch(t *testing.T) {
	fake := &fakeFeaturePubSub{
		features: nil,
		ready:    make(chan struct{}),
	}

	sub := NewDefaultBulkSubscriber(fake)

	const awaitDurationMs = 200

	flushed := make(chan struct{})
	bulkHandler := func(_ context.Context, msg *contribPubsub.BulkMessage) ([]contribPubsub.BulkSubscribeResponseEntry, error) {
		require.Len(t, msg.Entries, 1)
		close(flushed)
		return nil, nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	subErr := make(chan error, 1)
	go func() {
		subErr <- sub.BulkSubscribe(ctx, contribPubsub.SubscribeRequest{
			Topic: "test-topic",
			BulkSubscribeConfig: contribPubsub.BulkSubscribeConfig{
				MaxMessagesCount:   100,
				MaxAwaitDurationMs: awaitDurationMs,
			},
		}, bulkHandler)
	}()

	<-fake.ready

	start := time.Now()
	go func() { _ = fake.handler(ctx, &contribPubsub.NewMessage{Data: []byte(`{}`), Topic: "test-topic"}) }()

	select {
	case <-flushed:
	case <-time.After(time.Duration(awaitDurationMs*4) * time.Millisecond):
		t.Fatal("batch path should flush within 4x awaitDuration")
	}

	elapsed := time.Since(start)
	lower := time.Duration(awaitDurationMs) * time.Millisecond * 7 / 10
	require.GreaterOrEqual(t, elapsed, lower,
		"single message must wait ~awaitDuration in batch mode (no drain-and-flush regression); got %v", elapsed)

	cancel()
	<-subErr
}


