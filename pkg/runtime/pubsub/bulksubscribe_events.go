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
	"github.com/google/uuid"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
)

const (
	Metadata = "metadata"
	Entries  = "entries"
)

type BulkSubscribeMessageItem struct {
	EntryID     string            `json:"entryID"`
	Event       interface{}       `json:"event"`
	Metadata    map[string]string `json:"metadata"`
	ContentType string            `json:"contentType,omitempty"`
}

type BulkSubscribeEnvelope struct {
	ID        string
	Entries   []BulkSubscribeMessageItem
	Metadata  map[string]string
	Topic     string
	Pubsub    string
	EventType string
}

func NewBulkSubscribeEnvelope(req *BulkSubscribeEnvelope) map[string]interface{} {
	id := req.ID
	if id == "" {
		id = uuid.New().String()
	}
	eventType := req.EventType
	if eventType == "" {
		eventType = contribPubsub.DefaultBulkEventType
	}

	bulkSubEnvelope := map[string]interface{}{
		contribPubsub.IDField:     id,
		contribPubsub.TypeField:   eventType,
		contribPubsub.TopicField:  req.Topic,
		contribPubsub.PubsubField: req.Pubsub,
		Metadata:                  req.Metadata,
		Entries:                   req.Entries,
	}

	return bulkSubEnvelope
}
