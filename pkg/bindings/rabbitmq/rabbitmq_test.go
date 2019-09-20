package rabbitmq

import (
	"testing"

	"github.com/actionscore/actions/pkg/components/bindings"
	"github.com/stretchr/testify/assert"
)

func TestParseMetadata(t *testing.T) {
	m := bindings.Metadata{}
	m.Properties = map[string]string{"QueueName": "a", "Host": "a", "DeleteWhenUnused": "true", "Durable": "true"}
	r := RabbitMQ{}
	rm, err := r.getRabbitMQMetadata(m)
	assert.Nil(t, err)
	assert.Equal(t, "a", rm.QueueName)
	assert.Equal(t, "a", rm.Host)
	assert.Equal(t, true, rm.DeleteWhenUnused)
	assert.Equal(t, true, rm.Durable)

}
