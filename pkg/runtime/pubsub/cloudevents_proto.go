/*
Copyright 2026 The Dapr Authors
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
	"encoding/base64"
	"encoding/json"
	"fmt"

	cepb "github.com/dapr/dapr/pkg/proto/cloudevents/v1"
)

// ContentTypeCloudEventProtobuf is the content type for protobuf-encoded CloudEvents.
const ContentTypeCloudEventProtobuf = "application/cloudevents+protobuf"

// standardCloudEventFields are fields that map directly to CloudEvent proto fields
// and should not be included in the attributes map.
var standardCloudEventFields = map[string]bool{
	"id":          true,
	"source":      true,
	"specversion": true,
	"type":        true,
	"data":        true,
	"data_base64": true,
}

// CloudEventToProto converts an internal CloudEvent map to protobuf format.
func CloudEventToProto(ce map[string]interface{}) (*cepb.CloudEvent, error) {
	protoEvent := &cepb.CloudEvent{
		Id:          getStringField(ce, "id"),
		Source:      getStringField(ce, "source"),
		SpecVersion: getStringField(ce, "specversion"),
		Type:        getStringField(ce, "type"),
		Attributes:  make(map[string]*cepb.CloudEventAttributeValue),
	}

	// Map all non-standard fields to attributes (including datacontenttype, extensions)
	for k, v := range ce {
		if standardCloudEventFields[k] {
			continue
		}
		if attr := toAttributeValue(v); attr != nil {
			protoEvent.Attributes[k] = attr
		}
	}

	// Handle data field
	if data, ok := ce["data"]; ok && data != nil {
		switch d := data.(type) {
		case []byte:
			protoEvent.Data = &cepb.CloudEvent_BinaryData{BinaryData: d}
		case string:
			protoEvent.Data = &cepb.CloudEvent_TextData{TextData: d}
		default:
			// JSON-serialize complex types
			jsonBytes, err := json.Marshal(d)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal data: %w", err)
			}
			protoEvent.Data = &cepb.CloudEvent_TextData{TextData: string(jsonBytes)}
		}
	} else if dataB64, ok := ce["data_base64"]; ok && dataB64 != nil {
		if b64Str, ok := dataB64.(string); ok {
			decoded, err := base64.StdEncoding.DecodeString(b64Str)
			if err != nil {
				return nil, fmt.Errorf("failed to decode data_base64: %w", err)
			}
			protoEvent.Data = &cepb.CloudEvent_BinaryData{BinaryData: decoded}
		}
	}

	return protoEvent, nil
}

// getStringField extracts a string field from a CloudEvent map.
func getStringField(ce map[string]interface{}, field string) string {
	if v, ok := ce[field]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// toAttributeValue converts a Go value to a CloudEventAttributeValue.
func toAttributeValue(v interface{}) *cepb.CloudEventAttributeValue {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case string:
		return &cepb.CloudEventAttributeValue{
			Attr: &cepb.CloudEventAttributeValue_CeString{CeString: val},
		}
	case bool:
		return &cepb.CloudEventAttributeValue{
			Attr: &cepb.CloudEventAttributeValue_CeBoolean{CeBoolean: val},
		}
	case int:
		return &cepb.CloudEventAttributeValue{
			Attr: &cepb.CloudEventAttributeValue_CeInteger{CeInteger: int32(val)},
		}
	case int32:
		return &cepb.CloudEventAttributeValue{
			Attr: &cepb.CloudEventAttributeValue_CeInteger{CeInteger: val},
		}
	case int64:
		return &cepb.CloudEventAttributeValue{
			Attr: &cepb.CloudEventAttributeValue_CeInteger{CeInteger: int32(val)},
		}
	case float64:
		// JSON numbers come as float64, convert to int if whole number
		if val == float64(int32(val)) {
			return &cepb.CloudEventAttributeValue{
				Attr: &cepb.CloudEventAttributeValue_CeInteger{CeInteger: int32(val)},
			}
		}
		// Otherwise store as string representation
		return &cepb.CloudEventAttributeValue{
			Attr: &cepb.CloudEventAttributeValue_CeString{CeString: fmt.Sprintf("%v", val)},
		}
	case []byte:
		return &cepb.CloudEventAttributeValue{
			Attr: &cepb.CloudEventAttributeValue_CeBytes{CeBytes: val},
		}
	default:
		// For complex types, JSON-serialize to string
		if jsonBytes, err := json.Marshal(val); err == nil {
			return &cepb.CloudEventAttributeValue{
				Attr: &cepb.CloudEventAttributeValue_CeString{CeString: string(jsonBytes)},
			}
		}
		return nil
	}
}
