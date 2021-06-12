// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	contrib_pubsub "github.com/dapr/components-contrib/pubsub"

	"github.com/dapr/dapr/pkg/retry"
)

// PublisRequest is the runtime wrapper to allow handling retry settings.
type PublishRequest struct {
	PubsubName    string
	RetrySettings retry.Settings
	Topic         string
	Data          []byte
	Metadata      map[string]string
}

// Adapter is the interface for message buses.
type Adapter interface {
	GetPubSub(pubsubName string) contrib_pubsub.PubSub
	Publish(req *PublishRequest) error
}
