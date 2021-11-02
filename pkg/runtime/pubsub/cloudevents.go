// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"github.com/google/uuid"

	contrib_contenttype "github.com/dapr/components-contrib/contenttype"
	contrib_pubsub "github.com/dapr/components-contrib/pubsub"
)

// CloudEvent is a request object to create a Dapr compliant cloudevent.
type CloudEvent struct {
	ID              string
	Data            []byte
	Topic           string
	Pubsub          string
	DataContentType string
	TraceID         string
}

// NewCloudEvent encapusalates the creation of a Dapr cloudevent from an existing cloudevent or a raw payload.
func NewCloudEvent(req *CloudEvent) (map[string]interface{}, error) {
	if contrib_contenttype.IsCloudEventContentType(req.DataContentType) {
		return contrib_pubsub.FromCloudEvent(req.Data, req.Topic, req.Pubsub, req.TraceID)
	}
	return contrib_pubsub.NewCloudEventsEnvelope(uuid.New().String(), req.ID, contrib_pubsub.DefaultCloudEventType, "", req.Topic, req.Pubsub,
		req.DataContentType, req.Data, req.TraceID), nil
}
