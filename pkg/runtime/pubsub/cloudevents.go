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
	"github.com/mitchellh/mapstructure"

	contribContenttype "github.com/dapr/components-contrib/contenttype"
	contribPubsub "github.com/dapr/components-contrib/pubsub"
)

// CloudEvent is a request object to create a Dapr compliant cloudevent.
// The cloud event properties can manually be overwritten by using metadata beginning with "cloudevent." as prefix.
type CloudEvent struct {
	ID              string `mapstructure:"cloudevent.id"`
	Data            []byte `mapstructure:"-"` // cannot be overridden
	Topic           string `mapstructure:"-"` // cannot be overridden
	Pubsub          string `mapstructure:"-"` // cannot be overridden
	DataContentType string `mapstructure:"-"` // cannot be overridden
	TraceID         string `mapstructure:"cloudevent.traceid"`
	TraceState      string `mapstructure:"cloudevent.tracestate"`
	Source          string `mapstructure:"cloudevent.source"`
	Type            string `mapstructure:"cloudevent.type"`
	TraceParent     string `mapstructure:"cloudevent.traceparent"`
}

// NewCloudEvent encapsulates the creation of a Dapr cloudevent from an existing cloudevent or a raw payload.
func NewCloudEvent(req *CloudEvent, metadata map[string]string) (map[string]interface{}, error) {
	if contribContenttype.IsCloudEventContentType(req.DataContentType) {
		return contribPubsub.FromCloudEvent(req.Data, req.Topic, req.Pubsub, req.TraceID, req.TraceState)
	}

	// certain metadata beginning with "cloudevent." are considered overrides to the cloudevent envelope
	// we ignore any error here as the original cloud event envelope is still valid
	_ = mapstructure.WeakDecode(metadata, req) // allows ignoring of case

	// the final cloud event envelope contains both "traceid" and "traceparent" set to the same value (req.TraceID)
	// eventually "traceid" will be deprecated as it was superseded by "traceparent"
	// currently "traceparent" is not set by the pubsub component and can only set by the user via metadata override
	// therefore, if an override is set for "traceparent", we use it, otherwise we use the original or overridden "traceid" value
	if req.TraceParent != "" {
		req.TraceID = req.TraceParent
	}
	return contribPubsub.NewCloudEventsEnvelope(req.ID, req.Source, req.Type,
		"", req.Topic, req.Pubsub, req.DataContentType, req.Data, req.TraceID, req.TraceState), nil
}
