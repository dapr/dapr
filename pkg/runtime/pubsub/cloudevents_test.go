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
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCloudEvent(t *testing.T) {
	t.Run("raw payload", func(t *testing.T) {
		ce, err := NewCloudEvent(&CloudEvent{
			ID:              "",
			Source:          "a",
			Topic:           "b",
			Data:            []byte("hello"),
			Pubsub:          "c",
			DataContentType: "",
			TraceID:         "d",
			Type:            "custom-type",
		}, map[string]string{})
		require.NoError(t, err)
		assert.NotEmpty(t, ce["id"])                 // validates that the ID is generated
		assert.True(t, validUUID(ce["id"].(string))) // validates that the ID is a UUID
		assert.Equal(t, "a", ce["source"].(string))
		assert.Equal(t, "b", ce["topic"].(string))
		assert.Equal(t, "hello", ce["data"].(string))
		assert.Equal(t, "text/plain", ce["datacontenttype"].(string))
		assert.Equal(t, "d", ce["traceid"].(string))
		assert.Equal(t, "custom-type", ce["type"].(string))
	})

	t.Run("raw payload no data", func(t *testing.T) {
		ce, err := NewCloudEvent(&CloudEvent{
			ID:              "testid",
			Source:          "", // defaults to "Dapr"
			Topic:           "b",
			Pubsub:          "c",
			DataContentType: "", // defaults to "text/plain"
			TraceID:         "d",
			Type:            "", // defaults to "com.dapr.event.sent"
		}, map[string]string{})
		require.NoError(t, err)
		assert.Equal(t, "testid", ce["id"].(string))
		assert.Equal(t, "Dapr", ce["source"].(string))
		assert.Equal(t, "b", ce["topic"].(string))
		assert.Empty(t, ce["data"])
		assert.Equal(t, "text/plain", ce["datacontenttype"].(string))
		assert.Equal(t, "d", ce["traceid"].(string))
		assert.Equal(t, "com.dapr.event.sent", ce["type"].(string))
	})

	t.Run("cloud event metadata override", func(t *testing.T) {
		ce, err := NewCloudEvent(&CloudEvent{
			Topic:           "originaltopic",
			Pubsub:          "originalpubsub",
			DataContentType: "originaldatacontenttype",
			Data:            []byte("originaldata"),
		}, map[string]string{
			// these properties should not actually override anything
			"cloudevent.topic":           "overridetopic",
			"cloudevent.pubsub":          "overridepubsub",
			"cloudevent.data":            "overridedata",
			"cloudevent.datacontenttype": "overridedatacontenttype",
			// these properties should override
			"cloudevent.source":      "overridesource",
			"cloudevent.id":          "overrideid",
			"cloudevent.type":        "overridetype",
			"cloudevent.traceparent": "overridetraceparent",
			"cloudevent.tracestate":  "overridetracestate",
		})
		require.NoError(t, err)
		assert.Equal(t, "originalpubsub", ce["pubsubname"].(string))
		assert.Equal(t, "originaltopic", ce["topic"].(string))
		assert.Equal(t, "originaldata", ce["data"].(string))
		assert.Equal(t, "originaldatacontenttype", ce["datacontenttype"].(string))
		assert.Equal(t, "overridetraceparent", ce["traceid"].(string))
		assert.Equal(t, "overridetracestate", ce["tracestate"].(string))
		assert.Equal(t, "overridetype", ce["type"].(string))
		assert.Equal(t, "overridesource", ce["source"].(string))
		assert.Equal(t, "overrideid", ce["id"].(string))
		assert.Equal(t, "overridetraceparent", ce["traceparent"].(string))
		assert.Equal(t, "overridetracestate", ce["tracestate"].(string))
	})

	t.Run("custom cloudevent", func(t *testing.T) {
		m := map[string]interface{}{
			"specversion":     "1.0",
			"id":              "event",
			"datacontenttype": "text/plain",
			"data":            "world",
		}
		b, _ := json.Marshal(m)

		ce, err := NewCloudEvent(&CloudEvent{
			Data:            b,
			DataContentType: "application/cloudevents+json",
			Topic:           "topic1",
			TraceID:         "trace1",
			Pubsub:          "pubsub",
		}, map[string]string{})
		require.NoError(t, err)
		assert.Equal(t, "event", ce["id"].(string))
		assert.Equal(t, "world", ce["data"].(string))
		assert.Equal(t, "text/plain", ce["datacontenttype"].(string))
		assert.Equal(t, "topic1", ce["topic"].(string))
		assert.Equal(t, "trace1", ce["traceid"].(string))
		assert.Equal(t, "pubsub", ce["pubsubname"].(string))
	})
}

func validUUID(u string) bool {
	_, err := uuid.Parse(u)
	return err == nil
}
