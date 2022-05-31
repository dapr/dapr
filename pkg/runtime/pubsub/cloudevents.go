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
	TraceState      string
}

// NewCloudEvent encapsulates the creation of a Dapr cloudevent from an existing cloudevent or a raw payload.
func NewCloudEvent(req *CloudEvent) (map[string]interface{}, error) {
	if contrib_contenttype.IsCloudEventContentType(req.DataContentType) {
		return contrib_pubsub.FromCloudEvent(req.Data, req.Topic, req.Pubsub, req.TraceID, req.TraceState)
	}
	return contrib_pubsub.NewCloudEventsEnvelope(uuid.New().String(), req.ID, contrib_pubsub.DefaultCloudEventType,
		"", req.Topic, req.Pubsub, req.DataContentType, req.Data, req.TraceID, req.TraceState), nil
}
