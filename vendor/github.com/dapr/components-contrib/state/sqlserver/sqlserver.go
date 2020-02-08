// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package sqlserver

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode"

	"github.com/dapr/components-contrib/state"
	mssql "github.com/denisenkom/go-mssqldb"
)

// KeyType defines type of the table identifier
type KeyType string

// KeyTypeFromString tries to create a KeyType from a string value
func KeyTypeFromString(k string) (KeyType, error) {
	switch k {
	case string(StringKeyType):
		return StringKeyType, nil
	case string(UUIDKeyType):
		return UUIDKeyType, nil
	case string(IntegerKeyType):
		return IntegerKeyType, nil
	}

	return InvalidKeyType, errors.New("invalid key type")
}

const (
	// StringKeyType defines a key of type string
	StringKeyType KeyType = "string"

	// UUIDKeyType defines a key of type UUID/GUID
	UUIDKeyType KeyType = "uuid"

	//IntegerKeyType defines a key of type integer
	IntegerKeyType KeyType = "integer"

	//InvalidKeyType defines an invalid key type
	InvalidKeyType KeyType = "invalid"
)

const (
	connectionStringKey  = "connectionString"
	tableNameKey         = "tableName"
	schemaKey            = "schema"
	keyTypeKey           = "keyType"
	keyLengthKey         = "keyLength"
	indexedPropertiesKey = "indexedProperties"
	keyColumnName        = "Key"
	rowVersionColumnName = "RowVersion"

	defaultKeyLength = 200
	defaultSchema    = "dbo"
)

// NewSQLServerStateStore creates a new instance of a Sql Server transaction store
func NewSQLServerStateStore() *SQLServer {
	store := SQLServer{}
	store.migratorFactory = newMigration

	return &store
}

// IndexedProperty defines a indexed property
type IndexedProperty struct {
	ColumnName string `json:"column"`
	Property   string `json:"property"`
	Type       string `json:"type"`
}

// SQLServer defines a Ms SQL Server based state store
type SQLServer struct {
	connectionString  string
	tableName         string
	schema            string
	keyType           KeyType
	keyLength         int
	indexedProperties []IndexedProperty
	migratorFactory   func(*SQLServer) migrator

	bulkDeleteCommand        string
	itemRefTableTypeName     string
	upsertCommand            string
	getCommand               string
	deleteWithETagCommand    string
	deleteWithoutETagCommand string
}

func isLetterOrNumber(c rune) bool {
	return unicode.IsNumber(c) || unicode.IsLetter(c)
}

func isValidSQLName(s string) bool {
	for _, c := range s {
		if !(isLetterOrNumber(c) || (c == '_')) {
			return false
		}
	}
	return true
}

func isValidIndexedPropertyName(s string) bool {
	for _, c := range s {
		if !(isLetterOrNumber(c) || (c == '_') || (c == '.') || (c == '[') || (c == ']')) {
			return false
		}
	}
	return true
}

func isValidIndexedPropertyType(s string) bool {
	for _, c := range s {
		if !(isLetterOrNumber(c) || (c == '(') || (c == ')')) {
			return false
		}
	}
	return true
}

// Init initializes the SQL server state store
func (s *SQLServer) Init(metadata state.Metadata) error {
	if val, ok := metadata.Properties[connectionStringKey]; ok && val != "" {
		s.connectionString = val
	} else {
		return fmt.Errorf("missing connection string")
	}

	if val, ok := metadata.Properties[tableNameKey]; ok && val != "" {
		if !isValidSQLName(val) {
			return fmt.Errorf("invalid table name, accepted characters are (A-Z, a-z, 0-9, _)")
		}

		s.tableName = val
	} else {
		return fmt.Errorf("missing table name")
	}

	if val, ok := metadata.Properties[keyTypeKey]; ok && val != "" {
		kt, err := KeyTypeFromString(val)
		if err != nil {
			return err
		}
		s.keyType = kt
	} else {
		s.keyType = StringKeyType
	}

	if s.keyType == StringKeyType {
		if val, ok := metadata.Properties[keyLengthKey]; ok && val != "" {
			var err error
			s.keyLength, err = strconv.Atoi(val)
			if err != nil {
				return err
			}

			if s.keyLength <= 0 {
				return fmt.Errorf("invalid key length value of %d", s.keyLength)
			}
		} else {
			s.keyLength = defaultKeyLength
		}
	}

	if val, ok := metadata.Properties[schemaKey]; ok && val != "" {
		if !isValidSQLName(val) {
			return fmt.Errorf("invalid schema name, accepted characters are (A-Z, a-z, 0-9, _)")
		}
		s.schema = val
	} else {
		s.schema = defaultSchema
	}

	if val, ok := metadata.Properties[indexedPropertiesKey]; ok && val != "" {
		var indexedProperties []IndexedProperty
		err := json.Unmarshal([]byte(val), &indexedProperties)
		if err != nil {
			return err
		}

		for _, p := range indexedProperties {
			if p.ColumnName == "" {
				return errors.New("indexed property column cannot be empty")
			}

			if p.Property == "" {
				return errors.New("indexed property name cannot be empty")
			}

			if p.Type == "" {
				return errors.New("indexed property type cannot be empty")
			}

			if !isValidSQLName(p.ColumnName) {
				return fmt.Errorf("invalid indexed property column name, accepted characters are (A-Z, a-z, 0-9, _)")
			}

			if !isValidIndexedPropertyName(p.Property) {
				return fmt.Errorf("invalid indexed property name, accepted characters are (A-Z, a-z, 0-9, _, ., [, ])")
			}

			if !isValidIndexedPropertyType(p.Type) {
				return fmt.Errorf("invalid indexed property type, accepted characters are (A-Z, a-z, 0-9, _, (, ))")
			}
		}

		s.indexedProperties = indexedProperties
	}

	migration := s.migratorFactory(s)
	mr, err := migration.executeMigrations()
	if err != nil {
		return err
	}

	s.itemRefTableTypeName = mr.itemRefTableTypeName
	s.bulkDeleteCommand = fmt.Sprintf("exec %s @itemsToDelete;", mr.bulkDeleteProcFullName)
	s.upsertCommand = mr.upsertProcFullName
	s.getCommand = mr.getCommand
	s.deleteWithETagCommand = mr.deleteWithETagCommand
	s.deleteWithoutETagCommand = mr.deleteWithoutETagCommand

	return nil
}

// Multi performs multiple updates on a Sql server store
func (s *SQLServer) Multi(reqs []state.TransactionalRequest) error {
	var deletes []state.DeleteRequest
	var sets []state.SetRequest
	for _, req := range reqs {
		switch req.Operation {
		case state.Upsert:
			setReq, ok := req.Request.(state.SetRequest)
			if !ok {
				return fmt.Errorf("expecting set request")
			}

			if setReq.Key == "" {
				return fmt.Errorf("missing key in upsert operation")
			}

			sets = append(sets, setReq)

		case state.Delete:

			delReq, ok := req.Request.(state.DeleteRequest)
			if !ok {
				return fmt.Errorf("expecting delete request")
			}

			if delReq.Key == "" {
				return fmt.Errorf("missing key in upsert operation")
			}

			deletes = append(deletes, delReq)

		default:
			return fmt.Errorf("unsupported operation: %s", req.Operation)
		}
	}

	if len(sets) > 0 || len(deletes) > 0 {
		return s.executeMulti(sets, deletes)
	}

	return nil
}

func (s *SQLServer) executeMulti(sets []state.SetRequest, deletes []state.DeleteRequest) error {
	db, err := sql.Open("sqlserver", s.connectionString)
	if err != nil {
		return err
	}

	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	if len(deletes) > 0 {
		err = s.executeBulkDelete(tx, deletes)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	if len(sets) > 0 {
		for _, set := range sets {
			err = s.executeSet(tx, &set)
			if err != nil {
				tx.Rollback()
				return err
			}
		}
	}

	return tx.Commit()
}

// Delete removes an entity from the store
func (s *SQLServer) Delete(req *state.DeleteRequest) error {
	db, err := sql.Open("sqlserver", s.connectionString)
	if err != nil {
		return err
	}

	defer db.Close()

	var res sql.Result
	if req.ETag != "" {
		var b []byte
		b, err = hex.DecodeString(req.ETag)
		if err != nil {
			return err
		}

		res, err = db.Exec(s.deleteWithETagCommand, sql.Named(keyColumnName, req.Key), sql.Named(rowVersionColumnName, b))
	} else {
		res, err = db.Exec(s.deleteWithoutETagCommand, sql.Named(keyColumnName, req.Key))
	}

	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rows != 1 {
		return fmt.Errorf("items was not updated")
	}

	return nil
}

// TvpDeleteTableStringKey defines a table type with string key
type TvpDeleteTableStringKey struct {
	ID         string
	RowVersion []byte
}

// BulkDelete removes multiple entries from the store
func (s *SQLServer) BulkDelete(req []state.DeleteRequest) error {
	db, err := sql.Open("sqlserver", s.connectionString)
	if err != nil {
		return err
	}

	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	err = s.executeBulkDelete(tx, req)
	if err != nil {
		tx.Rollback()
		return err
	}

	tx.Commit()

	return nil
}

func (s *SQLServer) executeBulkDelete(db dbExecutor, req []state.DeleteRequest) error {
	values := make([]TvpDeleteTableStringKey, len(req))
	for i, d := range req {
		var etag []byte
		var err error
		if d.ETag != "" {
			etag, err = hex.DecodeString(d.ETag)
			if err != nil {
				return err
			}
		}
		values[i] = TvpDeleteTableStringKey{ID: d.Key, RowVersion: etag}
	}

	itemsToDelete := mssql.TVP{
		TypeName: s.itemRefTableTypeName,
		Value:    values,
	}

	res, err := db.Exec(s.bulkDeleteCommand, sql.Named("itemsToDelete", itemsToDelete))
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if int(rows) != len(req) {
		err = fmt.Errorf("delete affected only %d rows, expected %d", rows, len(req))
		return err
	}

	return nil
}

// Get returns an entity from store
func (s *SQLServer) Get(req *state.GetRequest) (*state.GetResponse, error) {
	db, err := sql.Open("sqlserver", s.connectionString)
	if err != nil {
		return nil, err
	}

	defer db.Close()

	rows, err := db.Query(s.getCommand, sql.Named(keyColumnName, req.Key))

	if err != nil {
		return nil, err
	}

	if rows.Err() != nil {
		return nil, rows.Err()
	}

	defer rows.Close()

	if !rows.Next() {
		return nil, errors.New("not found")
	}

	var data string
	var rowVersion []byte
	err = rows.Scan(&data, &rowVersion)
	if err != nil {
		return nil, err
	}

	etag := hex.EncodeToString(rowVersion)

	return &state.GetResponse{
		Data: []byte(data),
		ETag: etag,
	}, nil
}

// Set adds/updates an entity on store
func (s *SQLServer) Set(req *state.SetRequest) error {
	db, err := sql.Open("sqlserver", s.connectionString)
	if err != nil {
		return err
	}

	defer db.Close()

	return s.executeSet(db, req)
}

// dbExecutor implements a common functionality implemented by db or tx
type dbExecutor interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

func (s *SQLServer) executeSet(db dbExecutor, req *state.SetRequest) error {
	json, err := json.Marshal(req.Value)
	if err != nil {
		return err
	}

	etag := sql.Named(rowVersionColumnName, nil)
	if req.ETag != "" {
		var b []byte
		b, err = hex.DecodeString(req.ETag)
		if err != nil {
			return err
		}
		etag.Value = b
	}
	res, err := db.Exec(s.upsertCommand, sql.Named(keyColumnName, req.Key), sql.Named("Data", string(json)), etag)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rows != 1 {
		return fmt.Errorf("no item was updated")
	}

	return nil
}

// BulkSet adds/updates multiple entities on store
func (s *SQLServer) BulkSet(req []state.SetRequest) error {
	db, err := sql.Open("sqlserver", s.connectionString)
	if err != nil {
		return err
	}

	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	for _, r := range req {
		err = s.executeSet(tx, &r)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	err = tx.Commit()
	return err
}
