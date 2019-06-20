//
// This project start as a fork of `github.com/nerdylikeme/go-documentdb` version
// but changed, and may be changed later
//
// Goal: add the full functionality of documentdb, align with the other sdks
// and make it more testable
//
package documentdb

import "reflect"

type RequestOptions struct {
	PartitionKey interface{}
}

type Config struct {
	MasterKey string
}

type DocumentDB struct {
	client Clienter
}

// Create DocumentDBClient
func New(url string, config Config) *DocumentDB {
	client := &Client{}
	client.Url = url
	client.Config = config
	return &DocumentDB{client}
}

// TODO: Add `requestOptions` arguments
// Read database by self link
func (c *DocumentDB) ReadDatabase(link string) (db *Database, err error) {
	err = c.client.Read(link, &db)
	if err != nil {
		return nil, err
	}
	return
}

// Read collection by self link
func (c *DocumentDB) ReadCollection(link string) (coll *Collection, err error) {
	err = c.client.Read(link, &coll)
	if err != nil {
		return nil, err
	}
	return
}

// Read document by self link
func (c *DocumentDB) ReadDocument(link string, doc interface{}) (err error) {
	err = c.client.Read(link, &doc)
	return
}

// Read sporc by self link
func (c *DocumentDB) ReadStoredProcedure(link string) (sproc *Sproc, err error) {
	err = c.client.Read(link, &sproc)
	if err != nil {
		return nil, err
	}
	return
}

// Read udf by self link
func (c *DocumentDB) ReadUserDefinedFunction(link string) (udf *UDF, err error) {
	err = c.client.Read(link, &udf)
	if err != nil {
		return nil, err
	}
	return
}

// Read all databases
func (c *DocumentDB) ReadDatabases() (dbs []Database, err error) {
	return c.QueryDatabases("")
}

// Read all collections by db selflink
func (c *DocumentDB) ReadCollections(db string) (colls []Collection, err error) {
	return c.QueryCollections(db, "")
}

// Read all sprocs by collection self link
func (c *DocumentDB) ReadStoredProcedures(coll string) (sprocs []Sproc, err error) {
	return c.QueryStoredProcedures(coll, "")
}

// Read all udfs by collection self link
func (c *DocumentDB) ReadUserDefinedFunctions(coll string) (udfs []UDF, err error) {
	return c.QueryUserDefinedFunctions(coll, "")
}

// Read all collection documents by self link
// TODO: use iterator for heavy transactions
func (c *DocumentDB) ReadDocuments(coll string, docs interface{}) (err error) {
	return c.QueryDocuments(coll, "", docs)
}

// Read all databases that satisfy a query
func (c *DocumentDB) QueryDatabases(query string) (dbs []Database, err error) {
	data := struct {
		Databases []Database `json:"Databases,omitempty"`
		Count     int        `json:"_count,omitempty"`
	}{}
	if len(query) > 0 {
		err = c.client.Query("dbs", query, &data)
	} else {
		err = c.client.Read("dbs", &data)
	}
	if dbs = data.Databases; err != nil {
		dbs = nil
	}
	return
}

// Read all db-collection that satisfy a query
func (c *DocumentDB) QueryCollections(db, query string) (colls []Collection, err error) {
	data := struct {
		Collections []Collection `json:"DocumentCollections,omitempty"`
		Count       int          `json:"_count,omitempty"`
	}{}
	if len(query) > 0 {
		err = c.client.Query(db+"colls/", query, &data)
	} else {
		err = c.client.Read(db+"colls/", &data)
	}
	if colls = data.Collections; err != nil {
		colls = nil
	}
	return
}

// Read all collection `sprocs` that satisfy a query
func (c *DocumentDB) QueryStoredProcedures(coll, query string) (sprocs []Sproc, err error) {
	data := struct {
		Sprocs []Sproc `json:"StoredProcedures,omitempty"`
		Count  int     `json:"_count,omitempty"`
	}{}
	if len(query) > 0 {
		err = c.client.Query(coll+"sprocs/", query, &data)
	} else {
		err = c.client.Read(coll+"sprocs/", &data)
	}
	if sprocs = data.Sprocs; err != nil {
		sprocs = nil
	}
	return
}

// Read all collection `udfs` that satisfy a query
func (c *DocumentDB) QueryUserDefinedFunctions(coll, query string) (udfs []UDF, err error) {
	data := struct {
		Udfs  []UDF `json:"UserDefinedFunctions,omitempty"`
		Count int   `json:"_count,omitempty"`
	}{}
	if len(query) > 0 {
		err = c.client.Query(coll+"udfs/", query, &data)
	} else {
		err = c.client.Read(coll+"udfs/", &data)
	}
	if udfs = data.Udfs; err != nil {
		udfs = nil
	}
	return
}

// Read all documents in a collection that satisfy a query
func (c *DocumentDB) QueryDocuments(coll, query string, docs interface{}) (err error) {
	data := struct {
		Documents interface{} `json:"Documents,omitempty"`
		Count     int         `json:"_count,omitempty"`
	}{Documents: docs}
	if len(query) > 0 {
		err = c.client.Query(coll+"docs/", query, &data)
	} else {
		err = c.client.Read(coll+"docs/", &data)
	}
	return
}

// Read all documents in a collection that satisfy a query and with request options
func (c *DocumentDB) QueryDocumentsWithRequestOptions(coll, query string, docs interface{}, requestOptions ...func(*RequestOptions)) (err error) {
	data := struct {
		Documents interface{} `json:"Documents,omitempty"`
		Count     int         `json:"_count,omitempty"`
	}{Documents: docs}
	// If there are request options
	if len(requestOptions) > 0 {
		if len(query) > 0 {
			err = c.client.QueryWithRequestOptions(coll+"docs/", query, &data, requestOptions)
		} else {
			err = c.client.ReadWithRequestOptions(coll+"docs/", &data, requestOptions)
		}
	} else {
		if len(query) > 0 {
			err = c.client.Query(coll+"docs/", query, &data)
		} else {
			err = c.client.Read(coll+"docs/", &data)
		}
	}
	return
}

// Create database
func (c *DocumentDB) CreateDatabase(body interface{}) (db *Database, err error) {
	err = c.client.Create("dbs", body, &db)
	if err != nil {
		return nil, err
	}
	return
}

// Create collection
func (c *DocumentDB) CreateCollection(db string, body interface{}) (coll *Collection, err error) {
	err = c.client.Create(db+"colls/", body, &coll)
	if err != nil {
		return nil, err
	}
	return
}

// Create stored procedure
func (c *DocumentDB) CreateStoredProcedure(coll string, body interface{}) (sproc *Sproc, err error) {
	err = c.client.Create(coll+"sprocs/", body, &sproc)
	if err != nil {
		return nil, err
	}
	return
}

// Create user defined function
func (c *DocumentDB) CreateUserDefinedFunction(coll string, body interface{}) (udf *UDF, err error) {
	err = c.client.Create(coll+"udfs/", body, &udf)
	if err != nil {
		return nil, err
	}
	return
}

// Create document
func (c *DocumentDB) CreateDocument(coll string, doc interface{}) error {
	id := reflect.ValueOf(doc).Elem().FieldByName("Id")
	if id.IsValid() && id.String() == "" {
		id.SetString(uuid())
	}
	return c.client.Create(coll+"docs/", doc, &doc)
}

// Upsert document
func (c *DocumentDB) UpsertDocument(coll string, doc interface{}) error {
	id := reflect.ValueOf(doc).Elem().FieldByName("Id")
	if id.IsValid() && id.String() == "" {
		id.SetString(uuid())
	}
	return c.client.Upsert(coll+"docs/", doc, &doc)
}

// TODO: DRY, but the sdk want that[mm.. maybe just client.Delete(self_link)]
// Delete database
func (c *DocumentDB) DeleteDatabase(link string) error {
	return c.client.Delete(link)
}

// Delete collection
func (c *DocumentDB) DeleteCollection(link string) error {
	return c.client.Delete(link)
}

// Delete document
func (c *DocumentDB) DeleteDocument(link string) error {
	return c.client.Delete(link)
}

// Delete stored procedure
func (c *DocumentDB) DeleteStoredProcedure(link string) error {
	return c.client.Delete(link)
}

// Delete user defined function
func (c *DocumentDB) DeleteUserDefinedFunction(link string) error {
	return c.client.Delete(link)
}

// Replace database
func (c *DocumentDB) ReplaceDatabase(link string, body interface{}) (db *Database, err error) {
	err = c.client.Replace(link, body, &db)
	if err != nil {
		return nil, err
	}
	return
}

// Replace document
func (c *DocumentDB) ReplaceDocument(link string, doc interface{}) error {
	return c.client.Replace(link, doc, &doc)
}

// Replace stored procedure
func (c *DocumentDB) ReplaceStoredProcedure(link string, body interface{}) (sproc *Sproc, err error) {
	err = c.client.Replace(link, body, &sproc)
	if err != nil {
		return nil, err
	}
	return
}

// Replace stored procedure
func (c *DocumentDB) ReplaceUserDefinedFunction(link string, body interface{}) (udf *UDF, err error) {
	err = c.client.Replace(link, body, &udf)
	if err != nil {
		return nil, err
	}
	return
}

// Execute stored procedure
func (c *DocumentDB) ExecuteStoredProcedure(link string, params, body interface{}) (err error) {
	err = c.client.Execute(link, params, &body)
	return
}
