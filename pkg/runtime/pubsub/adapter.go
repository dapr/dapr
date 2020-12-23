// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	contrib_pubsub "github.com/dapr/components-contrib/pubsub"
)

// Adapter is the interface for message buses
type Adapter interface {
	PubSubFeatures(pubsubName string) ([]contrib_pubsub.Feature, error)
	Publish(req *contrib_pubsub.PublishRequest) error
}
