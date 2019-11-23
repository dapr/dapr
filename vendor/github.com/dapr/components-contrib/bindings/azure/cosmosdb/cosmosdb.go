// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package cosmosdb

import (
	"encoding/json"
	"fmt"

	"github.com/a8m/documentdb"
	"github.com/dapr/components-contrib/bindings"
)

// CosmosDB allows performing state operations on collections
type CosmosDB struct {
	client       *documentdb.DocumentDB
	collection   *documentdb.Collection
	db           *documentdb.Database
	partitionKey string
}

type cosmosDBCredentials struct {
	URL          string `json:"url"`
	MasterKey    string `json:"masterKey"`
	Database     string `json:"database"`
	Collection   string `json:"collection"`
	PartitionKey string `json:"partitionKey"`
}

// NewCosmosDB returns a new CosmosDB instance
func NewCosmosDB() *CosmosDB {
	return &CosmosDB{}
}

// Init performs CosmosDB connection parsing and connecting
func (c *CosmosDB) Init(metadata bindings.Metadata) error {
	m, err := c.parseMetadata(metadata)
	if err != nil {
		return err
	}

	c.partitionKey = m.PartitionKey
	client := documentdb.New(m.URL, &documentdb.Config{
		MasterKey: &documentdb.Key{
			Key: m.MasterKey,
		},
	})

	dbs, err := client.QueryDatabases(&documentdb.Query{
		Query: "SELECT * FROM ROOT r WHERE r.id=@id",
		Parameters: []documentdb.Parameter{
			{Name: "@id", Value: m.Database},
		},
	})
	if err != nil {
		return err
	} else if len(dbs) == 0 {
		return fmt.Errorf("database %s for CosmosDB state store not found", m.Database)
	}

	c.db = &dbs[0]
	colls, err := client.QueryCollections(c.db.Self, &documentdb.Query{
		Query: "SELECT * FROM ROOT r WHERE r.id=@id",
		Parameters: []documentdb.Parameter{
			{Name: "@id", Value: m.Collection},
		},
	})
	if err != nil {
		return err
	} else if len(colls) == 0 {
		return fmt.Errorf("collection %s for CosmosDB state store not found", m.Collection)
	}

	c.collection = &colls[0]
	c.client = client
	return nil
}

func (c *CosmosDB) parseMetadata(metadata bindings.Metadata) (*cosmosDBCredentials, error) {
	connInfo := metadata.Properties
	b, err := json.Marshal(connInfo)
	if err != nil {
		return nil, err
	}

	var creds cosmosDBCredentials
	err = json.Unmarshal(b, &creds)
	if err != nil {
		return nil, err
	}
	return &creds, nil
}

func (c *CosmosDB) Write(req *bindings.WriteRequest) error {
	var obj interface{}
	err := json.Unmarshal(req.Data, &obj)
	if err != nil {
		return err
	}

	if val, ok := obj.(map[string]interface{})[c.partitionKey]; ok && val != "" {
		_, err = c.client.CreateDocument(c.collection.Self, obj, documentdb.PartitionKey(val))
		if err != nil {
			return err
		}

		return nil
	}
	return fmt.Errorf("missing partitionKey field %s from request body", c.partitionKey)
}
