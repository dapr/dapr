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

package postman

import (
	"context"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/todo"
)

// Interface the postman which is responsible for sending the pubsub messages
// to the channel sink.
type Interface interface {
	Deliver(context.Context, *pubsub.SubscribedMessage) error
	DeliverBulk(context.Context, *DelivererBulkRequest) error
}

// TODO: @joshvanl: consolidate struct types.
type DelivererBulkRequest struct {
	BulkSubCallData      *todo.BulkSubscribeCallData
	BulkSubMsg           *todo.BulkSubscribedMessage
	BulkSubResiliencyRes *todo.BulkSubscribeResiliencyRes
	BulkResponses        *[]contribpubsub.BulkSubscribeResponseEntry
	RawPayload           bool
	DeadLetterTopic      string
}
