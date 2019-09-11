//
// This project start as a fork of `github.com/nerdylikeme/go-documentdb` version
// but changed, and may be changed later
//
// Goal: add the full functionality of documentdb, align with the other sdks
// and make it more testable
//
package documentdb

import (
	"bytes"
	"net/http"
	"reflect"
	"sync"
)

var buffers = &sync.Pool{
	New: func() interface{} {
		return bytes.NewBuffer([]byte{})
	},
}

// IdentificationHydrator defines interface for ID hydrators
// that can prepopulate struct with default values
type IdentificationHydrator func(config *Config, doc interface{})

// DefaultIdentificationHydrator fills Id
func DefaultIdentificationHydrator(config *Config, doc interface{}) {
	id := reflect.ValueOf(doc).Elem().FieldByName(config.IdentificationPropertyName)
	if id.IsValid() && id.String() == "" {
		id.SetString(uuid())
	}
}

type Config struct {
	MasterKey                  *Key
	Client                     http.Client
	IdentificationHydrator     IdentificationHydrator
	IdentificationPropertyName string
}

func NewConfig(key *Key) *Config {
	return &Config{
		MasterKey:                  key,
		IdentificationHydrator:     DefaultIdentificationHydrator,
		IdentificationPropertyName: "Id",
	}
}

// WithClient stores given http client for later use by documentdb client.
func (c *Config) WithClient(client http.Client) *Config {
	c.Client = client
	return c
}

type DocumentDB struct {
	client Clienter
	config *Config
}

// New creates DocumentDBClient
func New(url string, config *Config) *DocumentDB {
	client := &Client{
		Client: config.Client,
	}
	client.Url = url
	client.Config = config
	return &DocumentDB{client: client, config: config}
}

// TODO: Add `requestOptions` arguments
// Read database by self link
func (c *DocumentDB) ReadDatabase(link string, opts ...CallOption) (db *Database, err error) {
	_, err = c.client.Read(link, &db, opts...)
	if err != nil {
		return nil, err
	}
	return
}

// Read collection by self link
func (c *DocumentDB) ReadCollection(link string, opts ...CallOption) (coll *Collection, err error) {
	_, err = c.client.Read(link, &coll, opts...)
	if err != nil {
		return nil, err
	}
	return
}

// Read document by self link
func (c *DocumentDB) ReadDocument(link string, doc interface{}, opts ...CallOption) (err error) {
	_, err = c.client.Read(link, &doc, opts...)
	return
}

// Read sporc by self link
func (c *DocumentDB) ReadStoredProcedure(link string, opts ...CallOption) (sproc *Sproc, err error) {
	_, err = c.client.Read(link, &sproc, opts...)
	if err != nil {
		return nil, err
	}
	return
}

// Read udf by self link
func (c *DocumentDB) ReadUserDefinedFunction(link string, opts ...CallOption) (udf *UDF, err error) {
	_, err = c.client.Read(link, &udf, opts...)
	if err != nil {
		return nil, err
	}
	return
}

// Read all databases
func (c *DocumentDB) ReadDatabases(opts ...CallOption) (dbs []Database, err error) {
	return c.QueryDatabases(nil, opts...)
}

// Read all collections by db selflink
func (c *DocumentDB) ReadCollections(db string, opts ...CallOption) (colls []Collection, err error) {
	return c.QueryCollections(db, nil, opts...)
}

// Read all sprocs by collection self link
func (c *DocumentDB) ReadStoredProcedures(coll string, opts ...CallOption) (sprocs []Sproc, err error) {
	return c.QueryStoredProcedures(coll, nil, opts...)
}

// Read pall udfs by collection self link
func (c *DocumentDB) ReadUserDefinedFunctions(coll string, opts ...CallOption) (udfs []UDF, err error) {
	return c.QueryUserDefinedFunctions(coll, nil, opts...)
}

// Read all collection documents by self link
// TODO: use iterator for heavy transactions
func (c *DocumentDB) ReadDocuments(coll string, docs interface{}, opts ...CallOption) (r *Response, err error) {
	return c.QueryDocuments(coll, nil, docs, opts...)
}

// Read all databases that satisfy a query
func (c *DocumentDB) QueryDatabases(query *Query, opts ...CallOption) (dbs Databases, err error) {
	data := struct {
		Databases Databases `json:"Databases,omitempty"`
		Count     int       `json:"_count,omitempty"`
	}{}
	if query != nil {
		_, err = c.client.Query("dbs", query, &data, opts...)
	} else {
		_, err = c.client.Read("dbs", &data, opts...)
	}
	if dbs = data.Databases; err != nil {
		dbs = nil
	}
	return
}

// Read all db-collection that satisfy a query
func (c *DocumentDB) QueryCollections(db string, query *Query, opts ...CallOption) (colls []Collection, err error) {
	data := struct {
		Collections []Collection `json:"DocumentCollections,omitempty"`
		Count       int          `json:"_count,omitempty"`
	}{}
	if query != nil {
		_, err = c.client.Query(db+"colls/", query, &data, opts...)
	} else {
		_, err = c.client.Read(db+"colls/", &data, opts...)
	}
	if colls = data.Collections; err != nil {
		colls = nil
	}
	return
}

// Read all collection `sprocs` that satisfy a query
func (c *DocumentDB) QueryStoredProcedures(coll string, query *Query, opts ...CallOption) (sprocs []Sproc, err error) {
	data := struct {
		Sprocs []Sproc `json:"StoredProcedures,omitempty"`
		Count  int     `json:"_count,omitempty"`
	}{}
	if query != nil {
		_, err = c.client.Query(coll+"sprocs/", query, &data, opts...)
	} else {
		_, err = c.client.Read(coll+"sprocs/", &data, opts...)
	}
	if sprocs = data.Sprocs; err != nil {
		sprocs = nil
	}
	return
}

// Read all collection `udfs` that satisfy a query
func (c *DocumentDB) QueryUserDefinedFunctions(coll string, query *Query, opts ...CallOption) (udfs []UDF, err error) {
	data := struct {
		Udfs  []UDF `json:"UserDefinedFunctions,omitempty"`
		Count int   `json:"_count,omitempty"`
	}{}
	if query != nil {
		_, err = c.client.Query(coll+"udfs/", query, &data, opts...)
	} else {
		_, err = c.client.Read(coll+"udfs/", &data, opts...)
	}
	if udfs = data.Udfs; err != nil {
		udfs = nil
	}
	return
}

// Read all documents in a collection that satisfy a query
func (c *DocumentDB) QueryDocuments(coll string, query *Query, docs interface{}, opts ...CallOption) (response *Response, err error) {
	data := struct {
		Documents interface{} `json:"Documents,omitempty"`
		Count     int         `json:"_count,omitempty"`
	}{Documents: docs}
	if query != nil {
		response, err = c.client.Query(coll+"docs/", query, &data, opts...)
	} else {
		response, err = c.client.Read(coll+"docs/", &data, opts...)
	}
	return
}

// Read collection's partition ranges
func (c *DocumentDB) QueryPartitionKeyRanges(coll string, query *Query, opts ...CallOption) (ranges []PartitionKeyRange, err error) {
	data := queryPartitionKeyRangesRequest{}
	if query != nil {
		_, err = c.client.Query(coll+"pkranges/", query, &data, opts...)
	} else {
		_, err = c.client.Read(coll+"pkranges/", &data, opts...)
	}
	if ranges = data.Ranges; err != nil {
		ranges = nil
	}
	return
}

// Create database
func (c *DocumentDB) CreateDatabase(body interface{}, opts ...CallOption) (db *Database, err error) {
	_, err = c.client.Create("dbs", body, &db, opts...)
	if err != nil {
		return nil, err
	}
	return
}

// Create collection
func (c *DocumentDB) CreateCollection(db string, body interface{}, opts ...CallOption) (coll *Collection, err error) {
	_, err = c.client.Create(db+"colls/", body, &coll, opts...)
	if err != nil {
		return nil, err
	}
	return
}

// Create stored procedure
func (c *DocumentDB) CreateStoredProcedure(coll string, body interface{}, opts ...CallOption) (sproc *Sproc, err error) {
	_, err = c.client.Create(coll+"sprocs/", body, &sproc, opts...)
	if err != nil {
		return nil, err
	}
	return
}

// Create user defined function
func (c *DocumentDB) CreateUserDefinedFunction(coll string, body interface{}, opts ...CallOption) (udf *UDF, err error) {
	_, err = c.client.Create(coll+"udfs/", body, &udf, opts...)
	if err != nil {
		return nil, err
	}
	return
}

// Create document
func (c *DocumentDB) CreateDocument(coll string, doc interface{}, opts ...CallOption) (*Response, error) {
	if c.config != nil && c.config.IdentificationHydrator != nil {
		c.config.IdentificationHydrator(c.config, doc)
	}
	return c.client.Create(coll+"docs/", doc, &doc, opts...)
}

// Upsert document
func (c *DocumentDB) UpsertDocument(coll string, doc interface{}, opts ...CallOption) (*Response, error) {
	if c.config != nil && c.config.IdentificationHydrator != nil {
		c.config.IdentificationHydrator(c.config, doc)
	}
	return c.client.Upsert(coll+"docs/", doc, &doc, opts...)
}

// TODO: DRY, but the sdk want that[mm.. maybe just client.Delete(self_link)]
// Delete database
func (c *DocumentDB) DeleteDatabase(link string, opts ...CallOption) (*Response, error) {
	return c.client.Delete(link, opts...)
}

// Delete collection
func (c *DocumentDB) DeleteCollection(link string, opts ...CallOption) (*Response, error) {
	return c.client.Delete(link, opts...)
}

// Delete document
func (c *DocumentDB) DeleteDocument(link string, opts ...CallOption) (*Response, error) {
	return c.client.Delete(link, opts...)
}

// Delete stored procedure
func (c *DocumentDB) DeleteStoredProcedure(link string, opts ...CallOption) (*Response, error) {
	return c.client.Delete(link, opts...)
}

// Delete user defined function
func (c *DocumentDB) DeleteUserDefinedFunction(link string, opts ...CallOption) (*Response, error) {
	return c.client.Delete(link, opts...)
}

// Replace database
func (c *DocumentDB) ReplaceDatabase(link string, body interface{}, opts ...CallOption) (db *Database, err error) {
	_, err = c.client.Replace(link, body, &db)
	if err != nil {
		return nil, err
	}
	return
}

// Replace document
func (c *DocumentDB) ReplaceDocument(link string, doc interface{}, opts ...CallOption) (*Response, error) {
	return c.client.Replace(link, doc, &doc, opts...)
}

// Replace stored procedure
func (c *DocumentDB) ReplaceStoredProcedure(link string, body interface{}, opts ...CallOption) (sproc *Sproc, err error) {
	_, err = c.client.Replace(link, body, &sproc, opts...)
	if err != nil {
		return nil, err
	}
	return
}

// Replace stored procedure
func (c *DocumentDB) ReplaceUserDefinedFunction(link string, body interface{}, opts ...CallOption) (udf *UDF, err error) {
	_, err = c.client.Replace(link, body, &udf, opts...)
	if err != nil {
		return nil, err
	}
	return
}

// Execute stored procedure
func (c *DocumentDB) ExecuteStoredProcedure(link string, params, body interface{}, opts ...CallOption) (err error) {
	_, err = c.client.Execute(link, params, &body, opts...)
	return
}
