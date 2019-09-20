package eventhubs

import (
	"testing"

	"github.com/actionscore/actions/pkg/components/bindings"
	"github.com/stretchr/testify/assert"
)

func TestParseMetadata(t *testing.T) {
	m := bindings.Metadata{}
	m.Properties = map[string]string{"connectionString": "a", "consumerGroup": "a", "messageAge": "a"}
	eh := AzureEventHubs{}
	meta, err := eh.parseMetadata(m)
	assert.Nil(t, err)
	assert.Equal(t, "a", meta.ConnectionString)
	assert.Equal(t, "a", meta.ConsumerGroup)
	assert.Equal(t, "a", meta.MessageAge)
}
