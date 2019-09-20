package sqs

import (
	"testing"

	"github.com/actionscore/actions/pkg/components/bindings"
	"github.com/stretchr/testify/assert"
)

func TestParseMetadata(t *testing.T) {
	m := bindings.Metadata{}
	m.Properties = map[string]string{"QueueName": "a", "Region": "a", "AccessKey": "a", "SecretKey": "a"}
	s := AWSSQS{}
	sqsM, err := s.parseSQSMetadata(m)
	assert.Nil(t, err)
	assert.Equal(t, "a", sqsM.QueueName)
	assert.Equal(t, "a", sqsM.Region)
	assert.Equal(t, "a", sqsM.AccessKey)
	assert.Equal(t, "a", sqsM.SecretKey)
}
