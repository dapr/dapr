/*
Copyright 2025 The Dapr Authors
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

package todo

import (
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/runtime/pubsub"
)

// Message contains all the essential information related to a particular entry.
// This need to be maintained as a separate struct, as we need to filter out messages and
// their related info doing retries of resiliency support.
type Message struct {
	CloudEvent map[string]interface{}
	RawData    *pubsub.BulkSubscribeMessageItem
	Entry      *contribpubsub.BulkMessageEntry
}

// BulkSubscribedMessage contains all the essential information related to
// a bulk subscribe message.
type BulkSubscribedMessage struct {
	PubSubMessages []Message
	Topic          string
	Metadata       map[string]string
	Pubsub         string
	Path           string
	Length         int
}

// BulkSubIngressDiagnostics holds diagnostics information for bulk subscribe
// ingress.
type BulkSubIngressDiagnostics struct {
	StatusWiseDiag map[string]int64
	Elapsed        float64
	RetryReported  bool
}

// BulkSubscribeCallData holds data for a bulk subscribe call.
type BulkSubscribeCallData struct {
	BulkResponses   *[]contribpubsub.BulkSubscribeResponseEntry
	BulkSubDiag     *BulkSubIngressDiagnostics
	EntryIdIndexMap *map[string]int //nolint:stylecheck
	PsName          string
	Topic           string
}

type BulkSubscribeResiliencyRes struct {
	Entries  []contribpubsub.BulkSubscribeResponseEntry
	Envelope map[string]any
}
