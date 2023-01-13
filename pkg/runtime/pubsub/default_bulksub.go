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
	"golang.org/x/exp/maps"

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
// Dapr buffers messages in memory and calls the handler with a list of messages
// when the buffer is full or max await duration is reached.
func (p *defaultBulkSubscriber) BulkSubscribe(ctx context.Context, req contribPubsub.SubscribeRequest, handler contribPubsub.BulkHandler) error {
	cfg := contribPubsub.BulkSubscribeConfig{
		MaxMessagesCount:   utils.GetIntValOrDefault(req.BulkSubscribeConfig.MaxMessagesCount, defaultMaxMessagesCount),
		MaxAwaitDurationMs: utils.GetIntValOrDefault(req.BulkSubscribeConfig.MaxAwaitDurationMs, defaultMaxAwaitDurationMs),
	}

	msgCbChan := make(chan msgWithCallback, cfg.MaxMessagesCount)
	go processBulkMessages(ctx, req.Topic, msgCbChan, cfg, handler)

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

// processBulkMessages reads messages from msgChan and publishes them to a BulkHandler.
// It buffers messages in memory and publishes them in bulk.
func processBulkMessages(ctx context.Context, topic string, msgCbChan <-chan msgWithCallback, cfg contribPubsub.BulkSubscribeConfig, handler contribPubsub.BulkHandler) {
	messages := make([]contribPubsub.BulkMessageEntry, cfg.MaxMessagesCount)
	msgCbMap := make(map[string]func(error), cfg.MaxMessagesCount)

	ticker := time.NewTicker(time.Duration(cfg.MaxAwaitDurationMs) * time.Millisecond)
	defer ticker.Stop()

	n := 0
	for {
		select {
		case <-ctx.Done():
			flushMessages(ctx, topic, messages[:n], msgCbMap, handler)
			return
		case msgCb := <-msgCbChan:
			messages[n] = msgCb.msg
			n++
			msgCbMap[msgCb.msg.EntryId] = msgCb.cb
			if n >= cfg.MaxMessagesCount {
				flushMessages(ctx, topic, messages[:n], msgCbMap, handler)
				n = 0
				maps.Clear(msgCbMap)
			}
		case <-ticker.C:
			flushMessages(ctx, topic, messages[:n], msgCbMap, handler)
			n = 0
			maps.Clear(msgCbMap)
		}
	}
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
