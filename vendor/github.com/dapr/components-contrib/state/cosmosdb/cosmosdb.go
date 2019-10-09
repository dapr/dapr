// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package cosmosdb

import (
	"encoding/json"
	"fmt"

	jsoniter "github.com/json-iterator/go"

	"github.com/dapr/components-contrib/state"

	"github.com/a8m/documentdb"
)

// StateStore is a CosmosDB state store
type StateStore struct {
	client     *documentdb.DocumentDB
	collection *documentdb.Collection
	db         *documentdb.Database
}

type credentials struct {
	URL        string `json:"url"`
	MasterKey  string `json:"masterKey"`
	Database   string `json:"database"`
	Collection string `json:"collection"`
}

// CosmosItem is a wrapper around a CosmosDB document
type CosmosItem struct {
	documentdb.Document
	ID    string      `json:"id"`
	Value interface{} `json:"value"`
}

// NewCosmosDBStateStore returns a new CosmosDB state store
func NewCosmosDBStateStore() *StateStore {
	return &StateStore{}
}

// Init does metadata and connection parsing
func (c *StateStore) Init(metadata state.Metadata) error {
	connInfo := metadata.Properties
	b, err := json.Marshal(connInfo)
	if err != nil {
		return err
	}

	var creds credentials
	err = json.Unmarshal(b, &creds)
	if err != nil {
		return err
	}

	client := documentdb.New(creds.URL, &documentdb.Config{
		MasterKey: &documentdb.Key{
			Key: creds.MasterKey,
		},
	})

	dbs, err := client.QueryDatabases(&documentdb.Query{
		Query: fmt.Sprintf("SELECT * FROM ROOT r WHERE r.id='%s'", creds.Database),
	})
	if err != nil {
		return err
	} else if len(dbs) == 0 {
		return fmt.Errorf("Database %s for CosmosDB state store not found", creds.Database)
	}

	c.db = &dbs[0]
	colls, err := client.QueryCollections(c.db.Self, &documentdb.Query{
		Query: fmt.Sprintf("SELECT * FROM ROOT r WHERE r.id='%s'", creds.Collection),
	})
	if err != nil {
		return err
	} else if len(colls) == 0 {
		return fmt.Errorf("Collection %s for CosmosDB state store not found", creds.Collection)
	}

	c.collection = &colls[0]
	c.client = client

	return nil
}

// Get retreives a CosmosDB item
func (c *StateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
	key := req.Key

	items := []CosmosItem{}
	options := []documentdb.CallOption{documentdb.PartitionKey(req.Key)}
	if req.Options.Consistency == state.Strong {
		options = append(options, documentdb.ConsistencyLevel(documentdb.Strong))
	}
	if req.Options.Consistency == state.Eventual {
		options = append(options, documentdb.ConsistencyLevel(documentdb.Eventual))
	}

	_, err := c.client.QueryDocuments(
		c.collection.Self,
		documentdb.NewQuery("SELECT * FROM ROOT r WHERE r.id=@id", documentdb.P{"@id", key}),
		&items,
		options...,
	)
	if err != nil {
		return nil, err
	} else if len(items) == 0 {
		return nil, nil
	}

	b, err := jsoniter.ConfigFastest.Marshal(&items[0].Value)
	if err != nil {
		return nil, err
	}

	return &state.GetResponse{
		Data: b,
		ETag: items[0].Etag,
	}, nil
}

// Set saves a CosmosDB item
func (c *StateStore) Set(req *state.SetRequest) error {
	err := state.CheckSetRequestOptions(req)
	if err != nil {
		return err
	}

	options := []documentdb.CallOption{documentdb.PartitionKey(req.Key)}

	if req.ETag != "" {
		options = append(options, documentdb.IfMatch((req.ETag)))
	}
	if req.Options.Consistency == state.Strong {
		options = append(options, documentdb.ConsistencyLevel(documentdb.Strong))
	}
	if req.Options.Consistency == state.Eventual {
		options = append(options, documentdb.ConsistencyLevel(documentdb.Eventual))
	}

	_, err = c.client.UpsertDocument(c.collection.Self, CosmosItem{ID: req.Key, Value: req.Value}, options...)

	if err != nil {
		return err
	}

	return nil
}

// BulkSet performs a bulk set operation
func (c *StateStore) BulkSet(req []state.SetRequest) error {
	for _, s := range req {
		err := c.Set(&s)
		if err != nil {
			return err
		}
	}

	return nil
}

// Delete performs a delete operation
func (c *StateStore) Delete(req *state.DeleteRequest) error {
	err := state.CheckDeleteRequestOptions(req)
	if err != nil {
		return err
	}

	selfLink := fmt.Sprintf("dbs/%s/colls/%s/docs/%s", c.db.Id, c.collection.Id, req.Key)

	options := []documentdb.CallOption{documentdb.PartitionKey(req.Key)}

	if req.ETag != "" {
		options = append(options, documentdb.IfMatch((req.ETag)))
	}
	if req.Options.Consistency == state.Strong {
		options = append(options, documentdb.ConsistencyLevel(documentdb.Strong))
	}
	if req.Options.Consistency == state.Eventual {
		options = append(options, documentdb.ConsistencyLevel(documentdb.Eventual))
	}

	_, err = c.client.DeleteDocument(selfLink, options...)
	return err
}

// BulkDelete performs a bulk delete operation
func (c *StateStore) BulkDelete(req []state.DeleteRequest) error {
	for _, r := range req {
		err := c.Delete(&r)
		if err != nil {
			return err
		}
	}

	return nil
}
