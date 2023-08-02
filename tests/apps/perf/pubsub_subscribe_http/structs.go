/*
Copyright 2023 The Dapr Authors
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

package main

import (
	cloudevents "github.com/cloudevents/sdk-go/v2"
)

type subscription struct {
	PubsubName    string            `json:"pubsubName"`
	Topic         string            `json:"topic"`
	Route         string            `json:"route"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	BulkSubscribe bulkSubscribe     `json:"bulkSubscribe,omitempty"`
}

type bulkSubscribe struct {
	Enabled            bool  `json:"enabled"`
	MaxMessagesCount   int32 `json:"maxMessagesCount,omitempty"`
	MaxAwaitDurationMs int32 `json:"maxAwaitDurationMs,omitempty"`
}

type bulkSubscribeMessage struct {
	Entries    []bulkSubscribeMessageEntry `json:"entries"`
	PubsubName string                      `json:"pubsubName"`
	Topic      string                      `json:"topic"`
	Type       string                      `json:"type"`
}

type bulkSubscribeMessageEntry struct {
	EntryID  string            `json:"entryId"`
	Event    cloudevents.Event `json:"event"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type bulkSubscribeResponse struct {
	Statuses []bulkSubscribeResponseStatus `json:"statuses"`
}

type bulkSubscribeResponseStatus struct {
	EntryID string `json:"entryId"`
	Status  string `json:"status"`
}
