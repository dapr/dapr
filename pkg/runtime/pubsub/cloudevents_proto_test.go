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

// Ensure the cepb import is used
var _ *cepb.CloudEvent
