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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
	daprt "github.com/dapr/dapr/pkg/testing"
)

func TestBulkPublish_DefaultBulkPublisher(t *testing.T) {
	m := mock.Mock{}
	m.On("Publish", mock.Anything).Return(nil)
	mockPubSub := &daprt.MockPubSub{Mock: m}

	bulkPublisher := NewDefaultBulkPublisher(mockPubSub)

	req := &contribPubsub.BulkPublishRequest{
		Entries: []contribPubsub.BulkMessageEntry{
			{
				EntryID:     "78a48b5c-ff5a-4275-9bef-4a3bb8eefc3b",
				Event:       []byte("event1"),
				ContentType: "application/octet-stream",
				Metadata:    map[string]string{},
			},
			{
				EntryID:     "d64669e2-fab6-4452-a933-8de44e26ca02",
				Event:       []byte("event2"),
				ContentType: "application/octet-stream",
				Metadata:    map[string]string{},
			},
		},
		PubsubName: "pubsub",
		Topic:      "topic",
		Metadata:   map[string]string{},
	}

	tcs := []struct {
		name                   string
		bulkPublishSeriallyKey string
	}{
		{
			name:                   "bulkPublishSeriallyKey is set to true",
			bulkPublishSeriallyKey: "true",
		},
		{
			name:                   "bulkPublishSeriallyKey is not true",
			bulkPublishSeriallyKey: "false",
		},
	}

	for _, tc := range tcs {
		t.Run(fmt.Sprintf("publishes all messages in a request, %s", tc.name), func(t *testing.T) {
			req.Metadata[bulkPublishSeriallyKey] = tc.bulkPublishSeriallyKey
			res, err := bulkPublisher.BulkPublish(req)

			assert.NoError(t, err)
			assert.Len(t, res.Statuses, len(req.Entries))

			var pubRequests []*contribPubsub.PublishRequest

			// Assert that all Publish requests have the correct topic and pubsub name.
			for _, call := range mockPubSub.Calls {
				assert.Equal(t, "Publish", call.Method)

				pubReq, ok := call.Arguments.Get(0).(*contribPubsub.PublishRequest)
				assert.True(t, ok)

				assert.Equal(t, req.PubsubName, pubReq.PubsubName)
				assert.Equal(t, req.Topic, pubReq.Topic)

				pubRequests = append(pubRequests, pubReq)
			}

			// Assert that a Publish request should be there for the message that was in the bulk publish request.
			for _, entry := range req.Entries {
				assert.Contains(t, pubRequests, &contribPubsub.PublishRequest{
					Data:        entry.Event,
					ContentType: &entry.ContentType,
					Metadata:    entry.Metadata,
					PubsubName:  req.PubsubName,
					Topic:       req.Topic,
				})
			}

			var responseEntryIds []string
			for _, status := range res.Statuses {
				responseEntryIds = append(responseEntryIds, status.EntryID)
			}

			// Assert that response contains all entry IDs from the request.
			for _, entry := range req.Entries {
				assert.Contains(t, responseEntryIds, entry.EntryID)
			}
		})
	}
}
