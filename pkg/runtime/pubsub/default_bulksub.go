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
	"time"

	"github.com/google/uuid"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/utils"
)

const (
	defaultMaxMessagesCount   int = 100
	defaultMaxAwaitDurationMs int = 1 * 1000
)

// msgWithCallback is a wrapper around a message that includes a callback function
// that is called when the message is processed.
type msgWithCallback struct {
	msg contribPubsub.BulkMessageEntry
	cb  func(error)
}

// defaultBulkSubscriber is the default implementation of BulkSubscriber.
// It is used when the component does not implement BulkSubscriber.
type defaultBulkSubscriber struct {
	p contribPubsub.PubSub
}

// NewDefaultBulkSubscriber returns a new defaultBulkSubscriber from a PubSub.
func NewDefaultBulkSubscriber(p contribPubsub.PubSub) *defaultBulkSubscriber {
	return &defaultBulkSubscriber{
		p: p,
	}
}

// BulkSubscribe subscribes to a topic using a BulkHandler.
//
// The flushing policy is chosen from the upstream component's
// declared features:
//
//   - Default: messages are buffered and flushed when either
//     MaxMessagesCount is reached or MaxAwaitDurationMs elapses since
//     the first message of the current pending batch. The await timer
//     is restarted on every flush, so a leftover tail from a previous
//     batch gets the full window as part of the next pending batch.
//   - When the component declares pubsub.FeatureBulkSubscribeImmediate
//     (e.g. Pulsar): each message is flushed as soon as it arrives.
//     The component is signalling that its delivery model is
//     incompatible with a batching window — typically because the
//     broker delivers serially and blocks on ack — and that holding a
//     message would only delay the ack.
func (p *defaultBulkSubscriber) BulkSubscribe(ctx context.Context, req contribPubsub.SubscribeRequest, handler contribPubsub.BulkHandler) error {
	cfg := contribPubsub.BulkSubscribeConfig{
		MaxMessagesCount:   utils.GetIntValOrDefault(req.BulkSubscribeConfig.MaxMessagesCount, defaultMaxMessagesCount),
		MaxAwaitDurationMs: utils.GetIntValOrDefault(req.BulkSubscribeConfig.MaxAwaitDurationMs, defaultMaxAwaitDurationMs),
	}

	msgCbChan := make(chan msgWithCallback, cfg.MaxMessagesCount)
	if contribPubsub.FeatureBulkSubscribeImmediate.IsPresent(p.p.Features()) {
		// Warn if the user configured an explicit await window that
		// the immediate path will not honour. We check the raw input
		// (req.BulkSubscribeConfig) rather than cfg, so the
		// runtime-default substitution does not produce a spurious
		// warning for callers that left the field unset.
		if req.BulkSubscribeConfig.MaxAwaitDurationMs > 0 {
			bulkPSLogger.Warnf(
				"bulk subscribe on topic %q: maxAwaitDurationMs=%dms is ignored because the pubsub component declares immediate-flush; each message is delivered as soon as it arrives",
				req.Topic, req.BulkSubscribeConfig.MaxAwaitDurationMs)
		}
		go processBulkMessagesImmediate(ctx, req.Topic, msgCbChan, cfg, handler)
	} else {
		go processBulkMessagesBatch(ctx, req.Topic, msgCbChan, cfg, handler)
	}

	// Subscribe to the topic and listen for messages.
	return p.p.Subscribe(ctx, req, func(ctx context.Context, msg *contribPubsub.NewMessage) error {
		entryId, err := uuid.NewRandom() //nolint:stylecheck
		if err != nil {
			return err
		}

		bulkMsgEntry := contribPubsub.BulkMessageEntry{
			EntryId:  entryId.String(),
			Event:    msg.Data,
			Metadata: msg.Metadata,
		}

		if msg.ContentType != nil {
			bulkMsgEntry.ContentType = *msg.ContentType
		}

		done := make(chan struct{})

		msgCbChan <- msgWithCallback{
			msg: bulkMsgEntry,
			cb: func(ierr error) {
				err = ierr

				close(done)
			},
		}

		// Wait for the message to be processed.
		<-done

		return err
	})
}

// processBulkMessagesImmediate flushes the buffer as soon as any
// message arrives, without waiting for the count threshold or the
// await timer. It is selected when the upstream component declares
// pubsub.FeatureBulkSubscribeImmediate — typically because the broker
// delivers messages serially and blocks on ack, so a second message
// will never land for accumulation while the first is pending and the
// batching window cannot form. drainChannel is still applied, so any
// concurrently-queued siblings are coalesced into the same flush (up
// to MaxMessagesCount). "Immediate" describes *when* we flush, not
// the batch size.
func processBulkMessagesImmediate(ctx context.Context, topic string, msgCbChan <-chan msgWithCallback, cfg contribPubsub.BulkSubscribeConfig, handler contribPubsub.BulkHandler) {
	messages := make([]contribPubsub.BulkMessageEntry, cfg.MaxMessagesCount)
	msgCbMap := make(map[string]func(error), cfg.MaxMessagesCount)

	for {
		select {
		case <-ctx.Done():
			return
		case msgCb := <-msgCbChan:
			messages[0] = msgCb.msg
			msgCbMap[msgCb.msg.EntryId] = msgCb.cb
			n := 1
			n = drainChannel(msgCbChan, messages, msgCbMap, n, cfg.MaxMessagesCount)
			flushMessages(ctx, topic, messages[:n], msgCbMap, handler)
			clear(msgCbMap)
		}
	}
}

// processBulkMessagesBatch reads messages from msgChan and publishes them
// to a BulkHandler. It buffers messages in memory and publishes them in
// bulk when either MaxMessagesCount is reached or MaxAwaitDurationMs
// elapses since the first message of the current pending batch.
//
// The ticker is restarted in two cases so the await window is always
// measured from the first message of the pending batch:
//
//   - On every flush (count-based or timer-based), so a leftover tail
//     gets the full window as part of the next pending batch rather
//     than the stale remainder of the original tick.
//   - On the n==0 → n==1 transition, so a message arriving after a
//     period of inactivity is held for the full MaxAwaitDurationMs
//     instead of being flushed at the next periodic tick (which
//     would have been scheduled relative to a long-past anchor).
func processBulkMessagesBatch(ctx context.Context, topic string, msgCbChan <-chan msgWithCallback, cfg contribPubsub.BulkSubscribeConfig, handler contribPubsub.BulkHandler) {
	messages := make([]contribPubsub.BulkMessageEntry, cfg.MaxMessagesCount)
	msgCbMap := make(map[string]func(error), cfg.MaxMessagesCount)

	awaitDuration := time.Duration(cfg.MaxAwaitDurationMs) * time.Millisecond
	ticker := time.NewTicker(awaitDuration)
	defer ticker.Stop()

	n := 0

	// flush delivers the current buffer and restarts the await-duration
	// ticker so the next pending batch starts with a fresh window. Draining
	// any pending tick before the reset avoids a spurious empty flush on
	// the next select iteration when a tick fires concurrently with a
	// count-based flush.
	flush := func() {
		flushMessages(ctx, topic, messages[:n], msgCbMap, handler)
		n = 0
		clear(msgCbMap)
		select {
		case <-ticker.C:
		default:
		}
		ticker.Reset(awaitDuration)
	}

	for {
		select {
		case <-ctx.Done():
			flushMessages(ctx, topic, messages[:n], msgCbMap, handler)
			return
		case msgCb := <-msgCbChan:
			if n == 0 {
				// First message of a new pending batch: align the
				// await timer to this arrival so the full window is
				// measured from now, not from a stale anchor (the
				// goroutine start, the previous flush, or a no-op
				// tick). Drain any pending tick first so the next
				// select iteration doesn't see a spurious early
				// fire.
				select {
				case <-ticker.C:
				default:
				}
				ticker.Reset(awaitDuration)
			}

			messages[n] = msgCb.msg
			msgCbMap[msgCb.msg.EntryId] = msgCb.cb
			n++

			// Opportunistically drain any concurrently-queued messages up
			// to MaxMessagesCount. This lets a burst of arrivals count
			// toward the threshold in a single wake-up; it does not force
			// a flush on its own.
			n = drainChannel(msgCbChan, messages, msgCbMap, n, cfg.MaxMessagesCount)

			if n >= cfg.MaxMessagesCount {
				flush()
			}

		case <-ticker.C:
			if n > 0 {
				flush()
			}
		}
	}
}

// drainChannel performs a non-blocking read of any immediately-available
// messages from msgCbChan into messages/msgCbMap, up to max. Returns the
// updated count.
func drainChannel(msgCbChan <-chan msgWithCallback, messages []contribPubsub.BulkMessageEntry, msgCbMap map[string]func(error), count, max int) int {
	for count < max {
		select {
		case msgCb := <-msgCbChan:
			messages[count] = msgCb.msg
			msgCbMap[msgCb.msg.EntryId] = msgCb.cb
			count++
		default:
			return count
		}
	}
	return count
}

// flushMessages writes messages to a BulkHandler and clears the messages slice.
func flushMessages(ctx context.Context, topic string, messages []contribPubsub.BulkMessageEntry, msgCbMap map[string]func(error), handler contribPubsub.BulkHandler) {
	if len(messages) == 0 {
		return
	}

	responses, err := handler(ctx, &contribPubsub.BulkMessage{
		Topic:    topic,
		Metadata: map[string]string{},
		Entries:  messages,
	})
	if err != nil {
		if responses != nil {
			// invoke callbacks for each message
			for _, r := range responses {
				if cb, ok := msgCbMap[r.EntryId]; ok {
					cb(r.Error)
				}
			}
		} else {
			// all messages failed
			for _, cb := range msgCbMap {
				cb(err)
			}
		}
	} else {
		// no error has occurred
		for _, cb := range msgCbMap {
			cb(nil)
		}
	}
}
