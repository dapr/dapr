package pubsub

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateCloudEventsEnvelope(t *testing.T) {
	envelope := NewCloudEventsEnvelope("a", "source", "eventType", nil)
	assert.NotNil(t, envelope)
}

func TestCreateCloudEventsEnvelopeDefaults(t *testing.T) {
	t.Run("default event type", func(t *testing.T) {
		envelope := NewCloudEventsEnvelope("a", "source", "", nil)
		assert.Equal(t, DefaultCloudEventType, envelope.Type)
	})

	t.Run("non-default event type", func(t *testing.T) {
		envelope := NewCloudEventsEnvelope("a", "source", "e1", nil)
		assert.Equal(t, "e1", envelope.Type)
	})

	t.Run("spec version", func(t *testing.T) {
		envelope := NewCloudEventsEnvelope("a", "source", "", nil)
		assert.Equal(t, CloudEventsSpecVersion, envelope.SpecVersion)
	})

	t.Run("has data", func(t *testing.T) {
		envelope := NewCloudEventsEnvelope("a", "source", "", []byte("data"))
		assert.Equal(t, "data", envelope.Data.(string))
	})

	t.Run("string data content type", func(t *testing.T) {
		envelope := NewCloudEventsEnvelope("a", "source", "", []byte("data"))
		assert.Equal(t, "text/plain", envelope.DataContentType)
	})

	t.Run("json data content type", func(t *testing.T) {
		str := `{ "data": "1" }`
		envelope := NewCloudEventsEnvelope("a", "source", "", []byte(str))
		assert.Equal(t, "application/json", envelope.DataContentType)
	})
}
