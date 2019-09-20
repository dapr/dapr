package kafka

import (
	"testing"

	"github.com/actionscore/actions/pkg/components/bindings"
	"github.com/stretchr/testify/assert"
)

func TestParseMetadata(t *testing.T) {
	m := bindings.Metadata{}
	m.Properties = map[string]string{"consumerGroup": "a", "publishTopic": "a", "brokers": "a", "topics": "a"}
	k := Kafka{}
	meta, err := k.getKafkaMetadata(m)
	assert.Nil(t, err)
	assert.Equal(t, "a", meta.Brokers[0])
	assert.Equal(t, "a", meta.ConsumerGroup)
	assert.Equal(t, "a", meta.PublishTopic)
	assert.Equal(t, "a", meta.Topics[0])
}
