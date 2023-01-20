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
	contribContenttype "github.com/dapr/components-contrib/contenttype"
	contribPubsub "github.com/dapr/components-contrib/pubsub"
)

// CloudEvent is a request object to create a Dapr compliant cloudevent.
// The cloud event properties can manually be overwritten by using metadata beginning with "cloudevent-" as prefix.
type CloudEvent struct {
	ID              string `mapstructure:"cloudevent-id"`
	Data            []byte `mapstructure:"cloudevent-data"`
	Topic           string `mapstructure:"cloudevent-topic"`
	Pubsub          string `mapstructure:"cloudevent-pubsub"`
	DataContentType string `mapstructure:"cloudevent-datacontenttype"`
	TraceID         string `mapstructure:"cloudevent-traceid"`
	TraceState      string `mapstructure:"cloudevent-tracestate"`
	Source          string `mapstructure:"cloudevent-source"`
	Type            string `mapstructure:"cloudevent-type"`
	TraceParent     string `mapstructure:"cloudevent-traceparent"`
}

// NewCloudEvent encapsulates the creation of a Dapr cloudevent from an existing cloudevent or a raw payload.
func NewCloudEvent(req *CloudEvent) (map[string]interface{}, error) {
	if contribContenttype.IsCloudEventContentType(req.DataContentType) {
		return contribPubsub.FromCloudEvent(req.Data, req.Topic, req.Pubsub, req.TraceID, req.TraceState)
	}
	// ensures the trace ID data is overwritten correctly if desired
	if req.TraceID == "" && req.TraceParent != "" {
		req.TraceID = req.TraceParent
	}
	return contribPubsub.NewCloudEventsEnvelope(req.ID, req.Source, req.Type,
		"", req.Topic, req.Pubsub, req.DataContentType, req.Data, req.TraceID, req.TraceState), nil
}
