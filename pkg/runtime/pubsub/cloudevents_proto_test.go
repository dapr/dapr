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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cepb "github.com/dapr/dapr/pkg/proto/cloudevents/v1"
)

func TestContentTypeConstant(t *testing.T) {
	assert.Equal(t, "application/cloudevents+protobuf", ContentTypeCloudEventProtobuf)
}

func TestCloudEventToProto(t *testing.T) {
	t.Run("basic event with text data", func(t *testing.T) {
		ce := map[string]interface{}{
			"id":              "test-id-123",
			"source":          "test-source",
			"specversion":     "1.0",
			"type":            "com.dapr.test",
			"datacontenttype": "text/plain",
			"data":            "hello world",
		}

		proto, err := CloudEventToProto(ce)
		require.NoError(t, err)

		assert.Equal(t, "test-id-123", proto.Id)
		assert.Equal(t, "test-source", proto.Source)
		assert.Equal(t, "1.0", proto.SpecVersion)
		assert.Equal(t, "com.dapr.test", proto.Type)

		// Check datacontenttype in attributes
		attr, ok := proto.Attributes["datacontenttype"]
		require.True(t, ok)
		assert.Equal(t, "text/plain", attr.GetCeString())

		// Check data is text
		assert.Equal(t, "hello world", proto.GetTextData())
	})

	t.Run("event with binary data", func(t *testing.T) {
		ce := map[string]interface{}{
			"id":              "binary-id",
			"source":          "binary-source",
			"specversion":     "1.0",
			"type":            "com.dapr.binary",
			"datacontenttype": "application/octet-stream",
			"data":            []byte{0x01, 0x02, 0x03},
		}

		proto, err := CloudEventToProto(ce)
		require.NoError(t, err)

		assert.Equal(t, []byte{0x01, 0x02, 0x03}, proto.GetBinaryData())
	})

	t.Run("event with data_base64", func(t *testing.T) {
		ce := map[string]interface{}{
			"id":              "b64-id",
			"source":          "b64-source",
			"specversion":     "1.0",
			"type":            "com.dapr.b64",
			"datacontenttype": "application/octet-stream",
			"data_base64":     "AQID", // base64 of [0x01, 0x02, 0x03]
		}

		proto, err := CloudEventToProto(ce)
		require.NoError(t, err)

		assert.Equal(t, []byte{0x01, 0x02, 0x03}, proto.GetBinaryData())
	})

	t.Run("event with extension attributes", func(t *testing.T) {
		ce := map[string]interface{}{
			"id":          "ext-id",
			"source":      "ext-source",
			"specversion": "1.0",
			"type":        "com.dapr.ext",
			"traceid":     "trace-123",
			"traceparent": "00-abc-def-01",
			"topic":       "my-topic",
			"pubsubname":  "my-pubsub",
		}

		proto, err := CloudEventToProto(ce)
		require.NoError(t, err)

		assert.Equal(t, "trace-123", proto.Attributes["traceid"].GetCeString())
		assert.Equal(t, "00-abc-def-01", proto.Attributes["traceparent"].GetCeString())
		assert.Equal(t, "my-topic", proto.Attributes["topic"].GetCeString())
		assert.Equal(t, "my-pubsub", proto.Attributes["pubsubname"].GetCeString())
	})
}

func TestProtoToCloudEvent(t *testing.T) {
	t.Run("basic event with text data", func(t *testing.T) {
		proto := &cepb.CloudEvent{
			Id:          "test-id",
			Source:      "test-source",
			SpecVersion: "1.0",
			Type:        "com.dapr.test",
			Attributes: map[string]*cepb.CloudEventAttributeValue{
				"datacontenttype": {Attr: &cepb.CloudEventAttributeValue_CeString{CeString: "text/plain"}},
			},
			Data: &cepb.CloudEvent_TextData{TextData: "hello"},
		}

		ce, err := ProtoToCloudEvent(proto)
		require.NoError(t, err)

		assert.Equal(t, "test-id", ce["id"])
		assert.Equal(t, "test-source", ce["source"])
		assert.Equal(t, "1.0", ce["specversion"])
		assert.Equal(t, "com.dapr.test", ce["type"])
		assert.Equal(t, "text/plain", ce["datacontenttype"])
		assert.Equal(t, "hello", ce["data"])
	})

	t.Run("event with binary data", func(t *testing.T) {
		proto := &cepb.CloudEvent{
			Id:          "binary-id",
			Source:      "binary-source",
			SpecVersion: "1.0",
			Type:        "com.dapr.binary",
			Attributes: map[string]*cepb.CloudEventAttributeValue{
				"datacontenttype": {Attr: &cepb.CloudEventAttributeValue_CeString{CeString: "application/octet-stream"}},
			},
			Data: &cepb.CloudEvent_BinaryData{BinaryData: []byte{0x01, 0x02, 0x03}},
		}

		ce, err := ProtoToCloudEvent(proto)
		require.NoError(t, err)

		// Binary data should be base64 encoded in the map for JSON compatibility
		assert.Equal(t, "AQID", ce["data_base64"])
	})

	t.Run("event with extensions", func(t *testing.T) {
		proto := &cepb.CloudEvent{
			Id:          "ext-id",
			Source:      "ext-source",
			SpecVersion: "1.0",
			Type:        "com.dapr.ext",
			Attributes: map[string]*cepb.CloudEventAttributeValue{
				"traceid":    {Attr: &cepb.CloudEventAttributeValue_CeString{CeString: "trace-123"}},
				"topic":      {Attr: &cepb.CloudEventAttributeValue_CeString{CeString: "my-topic"}},
				"pubsubname": {Attr: &cepb.CloudEventAttributeValue_CeString{CeString: "my-pubsub"}},
				"myint":      {Attr: &cepb.CloudEventAttributeValue_CeInteger{CeInteger: 42}},
				"mybool":     {Attr: &cepb.CloudEventAttributeValue_CeBoolean{CeBoolean: true}},
			},
		}

		ce, err := ProtoToCloudEvent(proto)
		require.NoError(t, err)

		assert.Equal(t, "trace-123", ce["traceid"])
		assert.Equal(t, "my-topic", ce["topic"])
		assert.Equal(t, "my-pubsub", ce["pubsubname"])
		assert.Equal(t, int32(42), ce["myint"])
		assert.Equal(t, true, ce["mybool"])
	})
}

func TestSerializeDeserializeRoundTrip(t *testing.T) {
	t.Run("text data round trip", func(t *testing.T) {
		original := map[string]interface{}{
			"id":              "roundtrip-1",
			"source":          "test-source",
			"specversion":     "1.0",
			"type":            "com.dapr.test",
			"datacontenttype": "text/plain",
			"data":            "hello world",
			"traceid":         "trace-abc",
			"topic":           "my-topic",
		}

		data, err := SerializeCloudEventProto(original)
		require.NoError(t, err)
		require.NotEmpty(t, data)

		restored, err := DeserializeCloudEventProto(data)
		require.NoError(t, err)

		assert.Equal(t, original["id"], restored["id"])
		assert.Equal(t, original["source"], restored["source"])
		assert.Equal(t, original["specversion"], restored["specversion"])
		assert.Equal(t, original["type"], restored["type"])
		assert.Equal(t, original["datacontenttype"], restored["datacontenttype"])
		assert.Equal(t, original["data"], restored["data"])
		assert.Equal(t, original["traceid"], restored["traceid"])
		assert.Equal(t, original["topic"], restored["topic"])
	})

	t.Run("binary data round trip", func(t *testing.T) {
		original := map[string]interface{}{
			"id":              "roundtrip-2",
			"source":          "binary-source",
			"specversion":     "1.0",
			"type":            "com.dapr.binary",
			"datacontenttype": "application/octet-stream",
			"data":            []byte{0xDE, 0xAD, 0xBE, 0xEF},
		}

		data, err := SerializeCloudEventProto(original)
		require.NoError(t, err)

		restored, err := DeserializeCloudEventProto(data)
		require.NoError(t, err)

		// Binary data comes back as data_base64
		b64, ok := restored["data_base64"].(string)
		require.True(t, ok)
		decoded, err := base64.StdEncoding.DecodeString(b64)
		require.NoError(t, err)
		assert.Equal(t, original["data"], decoded)
	})
}

func TestDeserializeInvalidData(t *testing.T) {
	_, err := DeserializeCloudEventProto([]byte("not a valid protobuf"))
	assert.Error(t, err)
}

func TestProtobufCloudEventRoundTrip(t *testing.T) {
	t.Run("full CloudEvent with all Dapr fields", func(t *testing.T) {
		// Create a CloudEvent like Dapr would during publish
		original := map[string]interface{}{
			"id":              "event-12345",
			"source":          "my-app",
			"specversion":     "1.0",
			"type":            "com.dapr.event.sent",
			"datacontenttype": "application/json",
			"data":            `{"message":"hello world"}`,
			"topic":           "test-topic",
			"pubsubname":      "my-pubsub",
			"traceid":         "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			"traceparent":     "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			"tracestate":      "congo=t61rcWkgMzE",
		}

		// Serialize to protobuf (what publish does)
		protoBytes, err := SerializeCloudEventProto(original)
		require.NoError(t, err)
		require.NotEmpty(t, protoBytes)

		// Deserialize from protobuf (what subscribe does)
		restored, err := DeserializeCloudEventProto(protoBytes)
		require.NoError(t, err)

		// Verify all fields are preserved
		assert.Equal(t, original["id"], restored["id"])
		assert.Equal(t, original["source"], restored["source"])
		assert.Equal(t, original["specversion"], restored["specversion"])
		assert.Equal(t, original["type"], restored["type"])
		assert.Equal(t, original["datacontenttype"], restored["datacontenttype"])
		assert.Equal(t, original["data"], restored["data"])
		assert.Equal(t, original["topic"], restored["topic"])
		assert.Equal(t, original["pubsubname"], restored["pubsubname"])
		assert.Equal(t, original["traceid"], restored["traceid"])
		assert.Equal(t, original["traceparent"], restored["traceparent"])
		assert.Equal(t, original["tracestate"], restored["tracestate"])
	})

	t.Run("binary payload without base64 overhead on wire", func(t *testing.T) {
		// Large binary data that would be expensive as base64
		binaryData := make([]byte, 1000)
		for i := range binaryData {
			binaryData[i] = byte(i % 256)
		}

		original := map[string]interface{}{
			"id":              "binary-event",
			"source":          "sensor-device",
			"specversion":     "1.0",
			"type":            "com.sensor.reading",
			"datacontenttype": "application/octet-stream",
			"data":            binaryData,
		}

		// Serialize
		protoBytes, err := SerializeCloudEventProto(original)
		require.NoError(t, err)

		// Wire format should be smaller than JSON with base64
		// JSON: {"id":"binary-event",...,"data_base64":"<1336 chars>"} ≈ 1500+ bytes
		// Proto: id + source + type + spec + binary_data ≈ 1100 bytes
		assert.Less(t, len(protoBytes), 1150, "protobuf should be compact")

		// Deserialize
		restored, err := DeserializeCloudEventProto(protoBytes)
		require.NoError(t, err)

		// Binary data comes back as data_base64, decode to verify
		b64, ok := restored["data_base64"].(string)
		require.True(t, ok)
		decoded, err := base64.StdEncoding.DecodeString(b64)
		require.NoError(t, err)
		assert.Equal(t, binaryData, decoded)
	})
}

func TestMetadataConstants(t *testing.T) {
	assert.Equal(t, "cloudeventsFormat", MetadataKeyCloudEventsFormat)
	assert.Equal(t, "protobuf", CloudEventsFormatProtobuf)
}

func TestIsCloudEventProtobufContentType(t *testing.T) {
	tests := []struct {
		contentType string
		expected    bool
	}{
		{"application/cloudevents+protobuf", true},
		{"APPLICATION/CLOUDEVENTS+PROTOBUF", true},
		{"Application/CloudEvents+Protobuf", true},
		{"application/cloudevents+json", false},
		{"application/json", false},
		{"text/plain", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			result := IsCloudEventProtobufContentType(tt.contentType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Ensure the cepb import is used
var _ *cepb.CloudEvent
