package action

import (
	"encoding/json"
	"fmt"

	_ "github.com/a8m/documentdb" // documentdb go pkg fix
	documentdb "github.com/a8m/documentdb-go"
	"github.com/google/uuid"
)

type CosmosDB struct {
	client     *documentdb.DocumentDB
	collection *documentdb.Collection
	db         *documentdb.Database
}

func NewCosmosDB() *CosmosDB {
	return &CosmosDB{}
}

func (c *CosmosDB) Init(eventSourceSpec EventSourceSpec) error {
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

func (c *CosmosDB) Read(metadata interface{}) (interface{}, error) {
	return nil, nil
}

func (c *CosmosDB) ReadAsync(metadata interface{}, callback func([]byte) error) error {
	return nil
}

func (c *CosmosDB) Write(data interface{}) error {
	key := uuid.New()

	i := CosmosItem{
		ID:    key.String(),
		Value: data,
	}

	_, err := c.client.CreateDocument(c.collection.Self, i, documentdb.PartitionKey(key))
	if err != nil {
		return err
	}

	return nil
}
