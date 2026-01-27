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

// Ensure the cepb import is used
var _ *cepb.CloudEvent
