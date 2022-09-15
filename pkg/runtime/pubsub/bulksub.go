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

	"github.com/dapr/dapr/utils"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
)

const (
	maxBulkCountKey     string = "maxBulkCount"
	maxBulkLatencyMsKey string = "maxBulkLatencyMilliSeconds"

	defaultMaxBulkCount     int = 100
	defaultMaxBulkLatencyMs int = 1 * 1000
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
		MaxBulkCount:               utils.GetIntOrDefault(req.Metadata, maxBulkCountKey, defaultMaxBulkCount),
		MaxBulkLatencyMilliSeconds: utils.GetIntOrDefault(req.Metadata, maxBulkLatencyMsKey, defaultMaxBulkLatencyMs),
	}

	msgCbChan := make(chan msgWithCallback, cfg.MaxBulkCount)
	go processBulkMessages(ctx, req.Topic, msgCbChan, cfg, handler)

	// Subscribe to the topic and listen for messages.
	return p.p.Subscribe(ctx, req, func(ctx context.Context, msg *contribPubsub.NewMessage) error {
		var err error
		done := make(chan struct{})

		entryID, err := uuid.NewRandom()
		if err != nil {
			return err
		}

		bulkMsg := contribPubsub.BulkMessageEntry{
			EntryID:  entryID.String(),
			Event:    msg.Data,
			Metadata: msg.Metadata,
		}

		if msg.ContentType != nil {
			bulkMsg.ContentType = *msg.ContentType
		}

		msgCbChan <- msgWithCallback{
			msg: bulkMsg,
			cb: func(ierr error) {
				err = ierr
				done <- struct{}{}
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
	messages := make([]contribPubsub.BulkMessageEntry, 0, cfg.MaxBulkCount)
	msgCbMap := make(map[string]func(error))

	ticker := time.NewTicker(time.Duration(cfg.MaxBulkLatencyMilliSeconds) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			flushMessages(ctx, topic, messages, msgCbMap, handler)
			return
		case msgCb := <-msgCbChan:
			messages = append(messages, msgCb.msg)
			msgCbMap[msgCb.msg.EntryID] = msgCb.cb
			if len(messages) >= cfg.MaxBulkCount {
				messages, msgCbMap = flushMessages(ctx, topic, messages, msgCbMap, handler)
			}
		case <-ticker.C:
			messages, msgCbMap = flushMessages(ctx, topic, messages, msgCbMap, handler)
		}
	}
}

// flushMessages writes messages to a BulkHandler and clears the messages slice.
func flushMessages(ctx context.Context, topic string, messages []contribPubsub.BulkMessageEntry, msgCbMap map[string]func(error), handler contribPubsub.BulkHandler) (
	[]contribPubsub.BulkMessageEntry, map[string]func(error),
) {
	if len(messages) > 0 {
		responses, err := handler(ctx, &contribPubsub.BulkMessage{
			Topic:    topic,
			Metadata: map[string]string{},
			Entries:  messages,
		})

		if err != nil {
			if responses != nil {
				// invoke callbacks for each message
				for _, r := range responses {
					if cb, ok := (msgCbMap)[r.EntryID]; ok {
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

	return messages[:0], make(map[string]func(error))
}
