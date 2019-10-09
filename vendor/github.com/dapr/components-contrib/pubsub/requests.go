// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

// PublishRequest is the request to publish a message
type PublishRequest struct {
	Data  []byte `json:"data"`
	Topic string `json:"topic"`
}

// SubscribeRequest is the request to subscribe to a topic
type SubscribeRequest struct {
	Topic string `json:"topic"`
}

// NewMessage is an event arriving from a message bus instance
type NewMessage struct {
	Data  []byte `json:"data"`
	Topic string `json:"topic"`
}
