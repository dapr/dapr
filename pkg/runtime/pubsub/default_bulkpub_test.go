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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
	daprt "github.com/dapr/dapr/pkg/testing"
)

func TestBulkPublish_DefaultBulkPublisher(t *testing.T) {
	req := &contribPubsub.BulkPublishRequest{
		Entries: []contribPubsub.BulkMessageEntry{
			{
				EntryId:     "78a48b5c-ff5a-4275-9bef-4a3bb8eefc3b",
				Event:       []byte("event1"),
				ContentType: "text/plain",
				Metadata:    map[string]string{},
			},
			{
				EntryId:     "d64669e2-fab6-4452-a933-8de44e26ca02",
				Event:       []byte("event2"),
				ContentType: "text/plain",
				Metadata:    map[string]string{},
			},
			{
				EntryId:     "b3b4b2e1-2b9b-4b9b-9b9b-9b9b9b9b9b9b",
				Event:       []byte("event3"),
				ContentType: "text/plain",
				Metadata:    map[string]string{},
			},
		},
		PubsubName: "pubsub",
		Topic:      "topic",
		Metadata:   map[string]string{},
	}

	tcs := []struct {
		name                      string
		bulkPublishMaxConcurrency int
		publishErrors             []error
		expectError               bool
	}{
		{
			name:          "default bulk publish without publish errors",
			publishErrors: []error{nil, nil, nil},
			expectError:   false,
		},
		{
			name:          "default bulk publish with publish errors",
			publishErrors: []error{nil, errors.New("publish error"), nil},
			expectError:   true,
		},
	}

	for _, tc := range tcs {
		t.Run(fmt.Sprintf(tc.name), func(t *testing.T) {
			// Create publish requests for each message in the bulk request.
			var pubReqs []*contribPubsub.PublishRequest
			for _, entry := range req.Entries {
				contentType := entry.ContentType
				pubReqs = append(pubReqs, &contribPubsub.PublishRequest{
					Data:        entry.Event,
					ContentType: &contentType,
					Metadata:    entry.Metadata,
					PubsubName:  req.PubsubName,
					Topic:       req.Topic,
				})
			}

			// Set up the mock pubsub to return the publish errors.
			mockPubSub := &daprt.MockPubSub{Mock: mock.Mock{}}
			for i, e := range tc.publishErrors {
				mockPubSub.Mock.On("Publish", pubReqs[i]).Return(e)
			}
			bulkPublisher := NewDefaultBulkPublisher(mockPubSub)

			res, err := bulkPublisher.BulkPublish(context.Background(), req)

			// Check if the bulk publish method returns an error.
			if tc.expectError {
				assert.Error(t, err)
				// Response should contain an entry for each message in the bulk request.
				assert.Len(t, res.FailedEntries, len(req.Entries))
			} else {
				assert.NoError(t, err)
			}

			var pubInvocationArgs []*contribPubsub.PublishRequest

			// Assert that all Publish requests have the correct topic and pubsub name.
			for _, call := range mockPubSub.Calls {
				assert.Equal(t, "Publish", call.Method)

				pubReq, ok := call.Arguments.Get(0).(*contribPubsub.PublishRequest)
				assert.True(t, ok)

				assert.Equal(t, req.PubsubName, pubReq.PubsubName)
				assert.Equal(t, req.Topic, pubReq.Topic)

				pubInvocationArgs = append(pubInvocationArgs, pubReq)
			}

			// Assert that a Publish request should be there for the message that was in the bulk publish request.
			for _, pubReq := range pubReqs {
				assert.Contains(t, pubInvocationArgs, pubReq)
			}

			if tc.expectError {
				var responseEntryIds []string
				for _, status := range res.FailedEntries {
					responseEntryIds = append(responseEntryIds, status.EntryId)
				}
				// Assert that response contains all entry IDs from the request.
				for _, entry := range req.Entries {
					assert.Contains(t, responseEntryIds, entry.EntryId)
				}
			}
		})
	}
}
