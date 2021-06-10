// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	contrib_pubsub "github.com/dapr/components-contrib/pubsub"
)

// Adapter is the interface for message buses.
type Adapter interface {
	GetPubSub(pubsubName string) contrib_pubsub.PubSub
	Publish(req *contrib_pubsub.PublishRequest) error
}
