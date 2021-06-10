// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"fmt"

	"github.com/dapr/dapr/pkg/messages"
)

// pubsub.NotFoundError is returned by the runtime when the pubsub does not exist.
type NotFoundError struct {
	PubsubName string
}

func (e NotFoundError) Error() string {
	return fmt.Sprintf("pubsub '%s' not found", e.PubsubName)
}

// pubsub.NotAllowedError is returned by the runtime when publishing is forbidden.
type NotAllowedError struct {
	Topic string
	ID    string
}

func (e NotAllowedError) Error() string {
	return fmt.Sprintf(messages.ErrPubsubForbidden, e.Topic, e.ID)
}
