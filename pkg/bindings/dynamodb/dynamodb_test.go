package dynamodb

import (
	"testing"

	"github.com/actionscore/actions/pkg/components/bindings"
	"github.com/stretchr/testify/assert"
)

func TestParseMetadata(t *testing.T) {
	m := bindings.Metadata{}
	m.Properties = map[string]string{"AccessKey": "a", "Region": "a", "SecretKey": "a", "Table": "a"}
	dy := DynamoDB{}
	meta, err := dy.getDynamoDBMetadata(m)
	assert.Nil(t, err)
	assert.Equal(t, "a", meta.AccessKey)
	assert.Equal(t, "a", meta.Region)
	assert.Equal(t, "a", meta.SecretKey)
	assert.Equal(t, "a", meta.Table)
}
