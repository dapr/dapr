/*
Copyright 2021 The Dapr Authors
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

	contribPubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/outbox"
)

// Adapter is the interface for message buses.
type Adapter interface {
	Publish(context.Context, *contribPubsub.PublishRequest) error
	BulkPublish(context.Context, *contribPubsub.BulkPublishRequest) (contribPubsub.BulkPublishResponse, error)
	Outbox() outbox.Outbox
}
