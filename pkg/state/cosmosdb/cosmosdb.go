package cosmosdb

import (
	"encoding/json"
	"fmt"

	jsoniter "github.com/json-iterator/go"

	"github.com/actionscore/actions/pkg/components/state"

	documentdb "github.com/a8m/documentdb-go"
)

type CosmosDBStateStore struct {
	client     *documentdb.DocumentDB
	collection *documentdb.Collection
	db         *documentdb.Database
}

type CosmosDBCredentials struct {
	URL        string `json:"url"`
	MasterKey  string `json:"masterKey"`
	Database   string `json:"database"`
	Collection string `json:"collection"`
}

type CosmosItem struct {
	documentdb.Document
	ID    string      `json:"id"`
	Value interface{} `json:"value"`
}

func NewCosmosDBStateStore() *CosmosDBStateStore {
	return &CosmosDBStateStore{}
}

func (c *CosmosDBStateStore) Init(metadata state.Metadata) error {
	connInfo := metadata.ConnectionInfo
	b, err := json.Marshal(connInfo)
	if err != nil {
		return err
	}

	var creds CosmosDBCredentials
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

func (c *CosmosDBStateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
	key := req.Key

	items := []CosmosItem{}
	_, err := c.client.QueryDocuments(
		c.collection.Self,
		documentdb.NewQuery("SELECT * FROM ROOT r WHERE r.id=@id", documentdb.P{"@id", key}),
		&items,
		documentdb.PartitionKey(key),
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
	}, nil
}

func (c *CosmosDBStateStore) Set(req *state.SetRequest) error {
	items := []CosmosItem{}
	_, err := c.client.QueryDocuments(
		c.collection.Self,
		documentdb.NewQuery("SELECT * FROM ROOT r WHERE r.id=@id", documentdb.P{"@id", req.Key}),
		&items,
		documentdb.PartitionKey(req.Key),
	)
	if err != nil || len(items) > 0 {
		// Update
		i := items[0]
		i.Value = req.Value
		_, err := c.client.ReplaceDocument(i.Self, i, documentdb.PartitionKey(req.Key))
		if err != nil {
			return err
		}

	} else {
		// Create
		i := CosmosItem{
			ID:    req.Key,
			Value: req.Value,
		}

		_, err := c.client.CreateDocument(c.collection.Self, i, documentdb.PartitionKey(req.Key))
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *CosmosDBStateStore) BulkSet(req []state.SetRequest) error {
	for _, s := range req {
		err := c.Set(&s)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *CosmosDBStateStore) Delete(req *state.DeleteRequest) error {
	selfLink := fmt.Sprintf("dbs/%s/colls/%s/documents/%s", c.db.Id, c.collection.Id, req.Key)
	_, err := c.client.DeleteDocument(selfLink)
	return err
}

func (c *CosmosDBStateStore) BulkDelete(req []state.DeleteRequest) error {
	for _, r := range req {
		err := c.Delete(&r)
		if err != nil {
			return err
		}
	}

	return nil
}
