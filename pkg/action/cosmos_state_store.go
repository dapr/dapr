package action

import (
	"encoding/json"
	"fmt"
	"strings"

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

func (c *CosmosDBStateStore) Init(eventSourceSpec EventSourceSpec) error {
	connInfo := eventSourceSpec.ConnectionInfo
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

func (c *CosmosDBStateStore) ReadAsync(metadata interface{}, callback func([]byte) error) error {
	return nil
}

func (c *CosmosDBStateStore) GetAll(actionID string) ([]KeyValState, error) {
	state := []KeyValState{}
	items := []CosmosItem{}

	_, err := c.client.QueryDocuments(
		c.collection.Self,
		documentdb.NewQuery(fmt.Sprintf(`SELECT * FROM c WHERE CONTAINS(c.id, '%s')`, actionID)),
		&items,
		documentdb.CrossPartition(),
	)
	if err != nil {
		fmt.Println(err)
		return nil, err
	} else if len(items) == 0 {
		return nil, nil
	}

	for _, i := range items {
		state = append(state, KeyValState{
			Key:   strings.Replace(i.Id, fmt.Sprintf("%s-", actionID), "", -1),
			Value: i.Value,
		})
	}

	return state, nil
}

func (c *CosmosDBStateStore) Read(metadata interface{}) (interface{}, error) {
	key := metadata.(string)

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

	return items[0].Value, nil
}

func (c *CosmosDBStateStore) Write(data interface{}) error {
	state := data.(KeyValState)
	key := state.Key
	value := state.Value

	items := []CosmosItem{}
	_, err := c.client.QueryDocuments(
		c.collection.Self,
		documentdb.NewQuery("SELECT * FROM ROOT r WHERE r.id=@id", documentdb.P{"@id", key}),
		&items,
		documentdb.PartitionKey(key),
	)
	if err != nil || len(items) > 0 {
		// Update
		i := items[0]
		i.Value = value
		_, err := c.client.ReplaceDocument(i.Self, i, documentdb.PartitionKey(key))
		if err != nil {
			return err
		}

	} else {
		// Create
		i := CosmosItem{
			ID:    key,
			Value: value,
		}

		_, err := c.client.CreateDocument(c.collection.Self, i, documentdb.PartitionKey(key))
		if err != nil {
			return err
		}
	}

	return nil
}
