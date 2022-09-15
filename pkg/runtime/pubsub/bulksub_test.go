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
	"testing"

	"github.com/stretchr/testify/assert"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
)

func TestFlushMessages(t *testing.T) {
	emptyMessages := []contribPubsub.BulkMessageEntry{}
	sampleMessages := []contribPubsub.BulkMessageEntry{
		{EntryID: "1"},
		{EntryID: "2"},
	}

	emptyMsgCbMap := map[string]func(error){}
	sampleMsgCbMap := map[string]func(error){
		"1": func(err error) {},
		"2": func(err error) {},
	}

	t.Run("flushMessages should clear messages and msgCbMap", func(t *testing.T) {
		emptyHandler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
			[]contribPubsub.BulkSubscribeResponseEntry, error,
		) {
			return nil, nil
		}

		tests := []struct {
			name     string
			messages []contribPubsub.BulkMessageEntry
			msgCbMap map[string]func(error)
		}{
			{
				name:     "both messages and msgCbMap are already empty",
				messages: emptyMessages,
				msgCbMap: emptyMsgCbMap,
			},
			{
				name:     "messages is empty and msgCbMap is not empty",
				messages: emptyMessages,
				msgCbMap: sampleMsgCbMap,
			},
			{
				name:     "messages is not empty and msgCbMap is empty",
				messages: sampleMessages,
				msgCbMap: emptyMsgCbMap,
			},
			{
				name:     "both messages and msgCbMap are not empty",
				messages: sampleMessages,
				msgCbMap: sampleMsgCbMap,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				messages, msgCbMap := flushMessages(context.Background(), "topic", tc.messages, tc.msgCbMap, emptyHandler)
				assert.Equal(t, 0, len(messages))
				assert.Equal(t, 0, len(msgCbMap))
			})
		}
	})

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
					assert.Equal(t, len(tc.messages), len(msg.Entries))
					for _, entry := range msg.Entries {
						assert.Contains(t, tc.messages, entry)
					}
					return nil, nil
				}

				flushMessages(context.Background(), "topic", tc.messages, tc.msgCbMap, handler)
				assert.Equal(t, handlerInvoked, tc.expectedHandlerInvoked)
			})
		}
	})

	t.Run("flushMessages should invoke callbacks based on handler response", func(t *testing.T) {
		messages := []contribPubsub.BulkMessageEntry{
			{EntryID: "1"},
			{EntryID: "2"},
			{EntryID: "3"},
		}

		tests := []struct {
			name             string
			handlerResponses []contribPubsub.BulkSubscribeResponseEntry
			handlerErr       error
			entryIDErrMap    map[string]struct{}
		}{
			{
				"all callbacks should be invoked with nil error when handler returns nil error",
				[]contribPubsub.BulkSubscribeResponseEntry{
					{EntryID: "1"},
					{EntryID: "2"},
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
					{EntryID: "1", Error: errors.New("failed message")},
					{EntryID: "2"},
					{EntryID: "3", Error: errors.New("failed message")},
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

				flushMessages(context.Background(), "topic", messages, msgCbMap, handler)

				for id, err := range invokedCallbacks {
					if _, ok := tc.entryIDErrMap[id]; ok {
						assert.NotNil(t, err)
					} else {
						assert.Nil(t, err)
					}
				}
			})
		}
	})
}
