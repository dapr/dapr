package cosmosdb

import (
	"testing"

	"github.com/actionscore/actions/pkg/components/bindings"
	"github.com/stretchr/testify/assert"
)

func TestParseMetadata(t *testing.T) {
	m := bindings.Metadata{}
	m.Properties = map[string]string{"Collection": "a", "Database": "a", "MasterKey": "a", "PartitionKey": "a", "URL": "a"}
	cosmosDB := CosmosDB{}
	meta, err := cosmosDB.parseMetadata(m)
	assert.Nil(t, err)
	assert.Equal(t, "a", meta.Collection)
	assert.Equal(t, "a", meta.Database)
	assert.Equal(t, "a", meta.MasterKey)
	assert.Equal(t, "a", meta.PartitionKey)
	assert.Equal(t, "a", meta.URL)
}
