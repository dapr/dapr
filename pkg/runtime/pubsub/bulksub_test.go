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
	"testing"

	"github.com/stretchr/testify/assert"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
)

func TestFlushMessages(t *testing.T) {
	t.Run("Flush should clear messages and msgCbMap", func(t *testing.T) {
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

		emptyHandler := func(ctx context.Context, msg *contribPubsub.BulkMessage) (
			[]contribPubsub.BulkSubscribeResponseEntry, error) {
			return nil, nil
		}

		tcs := []struct {
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

		for _, tc := range tcs {
			messages, msgCbMap := flushMessages(context.Background(), "topic", tc.messages, tc.msgCbMap, emptyHandler)
			assert.Equal(t, 0, len(messages))
			assert.Equal(t, 0, len(msgCbMap))
		}
	})
}
