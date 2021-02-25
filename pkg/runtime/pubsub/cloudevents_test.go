// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package pubsub

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewCloudEvent(t *testing.T) {
	t.Run("raw payload", func(t *testing.T) {
		ce, err := NewCloudEvent(&CloudEvent{
			ID:              "a",
			Topic:           "b",
			Data:            []byte("hello"),
			Pubsub:          "c",
			DataContentType: "",
			TraceID:         "d",
		})
		assert.NoError(t, err)
		assert.Equal(t, "a", ce["source"].(string))
		assert.Equal(t, "b", ce["topic"].(string))
		assert.Equal(t, "hello", ce["data"].(string))
		assert.Equal(t, "text/plain", ce["datacontenttype"].(string))
		assert.Equal(t, "d", ce["traceid"].(string))
	})

	t.Run("raw payload no data", func(t *testing.T) {
		ce, err := NewCloudEvent(&CloudEvent{
			ID:              "a",
			Topic:           "b",
			Pubsub:          "c",
			DataContentType: "",
			TraceID:         "d",
		})
		assert.NoError(t, err)
		assert.Equal(t, "a", ce["source"].(string))
		assert.Equal(t, "b", ce["topic"].(string))
		assert.Empty(t, ce["data"])
		assert.Equal(t, "text/plain", ce["datacontenttype"].(string))
		assert.Equal(t, "d", ce["traceid"].(string))
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
		})
		assert.NoError(t, err)
		assert.Equal(t, "world", ce["data"].(string))
		assert.Equal(t, "text/plain", ce["datacontenttype"].(string))
		assert.Equal(t, "topic1", ce["topic"].(string))
		assert.Equal(t, "trace1", ce["traceid"].(string))
		assert.Equal(t, "pubsub", ce["pubsubname"].(string))
	})
}
