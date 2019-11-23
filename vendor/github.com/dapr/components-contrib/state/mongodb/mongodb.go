// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package mongodb

// mongodb package is an implementation of StateStore interface to perform operations on store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	json "github.com/json-iterator/go"

	"github.com/dapr/components-contrib/state"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

const (
	host             = "host"
	username         = "username"
	password         = "password"
	databaseName     = "databaseName"
	collectionName   = "collectionName"
	writeConcern     = "writeConcern"
	readConcern      = "readConcern"
	operationTimeout = "operationTimeout"
	id               = "_id"
	value            = "value"

	defaultTimeout        = 5 * time.Second
	defaultDatabaseName   = "daprStore"
	defaultCollectionName = "daprCollection"

	// mongodb://<username>:<password@<host>/<database>
	connectionURIFormat = "mongodb://%s:%s@%s/%s"
)

// MongoDB is a state store implementation for MongoDB
type MongoDB struct {
	client           *mongo.Client
	collection       *mongo.Collection
	operationTimeout time.Duration
}

type mongoDBMetadata struct {
	host             string
	username         string
	password         string
	databaseName     string
	collectionName   string
	writeconcern     string
	readconcern      string
	operationTimeout time.Duration
}

// Mongodb document wrapper
type Item struct {
	Key   string `bson:"_id"`
	Value string `bson:"value"`
}

// NewMongoDBStateStore returns a new MongoDB state store
func NewMongoDB() *MongoDB {
	return &MongoDB{}
}

// Init establishes connection to the store based on the metadata
func (m *MongoDB) Init(metadata state.Metadata) error {
	meta, err := getMongoDBMetaData(metadata)
	if err != nil {
		return err
	}

	client, err := getMongoDBClient(meta)

	if err != nil {
		return fmt.Errorf("error in creating mongodb client: %s", err)
	}

	m.client = client

	// get the write concern
	wc, err := getWriteConcernObject(meta.writeconcern)

	if err != nil {
		return fmt.Errorf("error in getting write concern object: %s", err)
	}

	// get the read concern
	rc, err := getReadConcernObject(meta.readconcern)

	if err != nil {
		return fmt.Errorf("error in getting read concern object: %s", err)
	}

	opts := options.Collection().SetWriteConcern(wc).SetReadConcern(rc)
	collection := m.client.Database(meta.databaseName).Collection(meta.collectionName, opts)

	m.collection = collection

	return nil
}

// Set saves state into MongoDB
func (m *MongoDB) Set(req *state.SetRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), m.operationTimeout)
	defer cancel()

	err := m.setInternal(ctx, req)

	if err != nil {
		return err
	}

	return nil
}

func (m *MongoDB) setInternal(ctx context.Context, req *state.SetRequest) error {
	var vStr string
	b, ok := req.Value.([]byte)
	if ok {
		vStr = string(b)
	} else {
		vStr, _ = json.MarshalToString(req.Value)
	}

	// create a document based on request key and value
	filter := bson.M{id: req.Key}
	update := bson.M{"$set": bson.M{id: req.Key, value: vStr}}
	_, err := m.collection.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))

	if err != nil {
		return err
	}

	return nil
}

// Get retrieves state from MongoDB with a key
func (m *MongoDB) Get(req *state.GetRequest) (*state.GetResponse, error) {
	var result Item

	ctx, cancel := context.WithTimeout(context.Background(), m.operationTimeout)
	defer cancel()

	filter := bson.M{id: req.Key}
	err := m.collection.FindOne(ctx, filter).Decode(&result)

	if err != nil {
		return &state.GetResponse{}, err
	}

	value := []byte(result.Value)

	return &state.GetResponse{
		Data: value,
	}, nil
}

// BulkSet performs a bulks save operation
func (m *MongoDB) BulkSet(req []state.SetRequest) error {
	for _, s := range req {
		err := m.Set(&s)
		if err != nil {
			return err
		}
	}

	return nil
}

// Delete performs a delete operation
func (m *MongoDB) Delete(req *state.DeleteRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), m.operationTimeout)
	defer cancel()

	err := m.deleteInternal(ctx, req)

	if err != nil {
		return err
	}

	return nil
}

func (m *MongoDB) deleteInternal(ctx context.Context, req *state.DeleteRequest) error {
	filter := bson.M{id: req.Key}
	_, err := m.collection.DeleteOne(ctx, filter)

	if err != nil {
		return err
	}

	return nil
}

// BulkDelete performs a bulk delete operation
func (m *MongoDB) BulkDelete(req []state.DeleteRequest) error {
	for _, r := range req {
		err := m.Delete(&r)
		if err != nil {
			return err
		}
	}

	return nil
}

// Multi performs a transactional operation. succeeds only if all operations succeed, and fails if one or more operations fail
func (m *MongoDB) Multi(operations []state.TransactionalRequest) error {
	sess, err := m.client.StartSession()
	txnOpts := options.Transaction().SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.New(writeconcern.WMajority()))

	defer sess.EndSession(context.Background())

	if err != nil {
		return fmt.Errorf("error in starting the transaction: %s", err)
	}

	sess.WithTransaction(context.Background(), func(sessCtx mongo.SessionContext) (interface{}, error) {
		err = m.doTransaction(sessCtx, operations)
		return nil, err
	}, txnOpts)

	return err
}

func (m *MongoDB) doTransaction(sessCtx mongo.SessionContext, operations []state.TransactionalRequest) error {
	for _, o := range operations {
		var err error
		if o.Operation == state.Upsert {
			req := o.Request.(state.SetRequest)
			err = m.setInternal(sessCtx, &req)
		} else if o.Operation == state.Delete {
			req := o.Request.(state.DeleteRequest)
			err = m.deleteInternal(sessCtx, &req)
		}

		if err != nil {
			sessCtx.AbortTransaction(sessCtx)
			return fmt.Errorf("error during transaction, aborting the transaction: %s", err)
		}
	}

	return nil
}

func getMongoDBClient(metadata *mongoDBMetadata) (*mongo.Client, error) {
	var uri string

	if metadata.username != "" && metadata.password != "" {
		uri = fmt.Sprintf(connectionURIFormat, metadata.username, metadata.password, metadata.host, metadata.databaseName)
	}

	// Set client options
	clientOptions := options.Client().ApplyURI(uri)

	// Connect to MongoDB
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	client, err := mongo.Connect(ctx, clientOptions)

	if err != nil {
		return nil, err
	}

	return client, nil
}

func getMongoDBMetaData(metadata state.Metadata) (*mongoDBMetadata, error) {
	meta := mongoDBMetadata{
		databaseName:     defaultDatabaseName,
		collectionName:   defaultCollectionName,
		operationTimeout: defaultTimeout,
	}

	if val, ok := metadata.Properties[host]; ok && val != "" {
		meta.host = val
	} else {
		return nil, errors.New("missing or empty host field from metadata")
	}

	if val, ok := metadata.Properties[username]; ok && val != "" {
		meta.username = val
	}

	if val, ok := metadata.Properties[password]; ok && val != "" {
		meta.password = val
	}

	if val, ok := metadata.Properties[databaseName]; ok && val != "" {
		meta.databaseName = val
	}

	if val, ok := metadata.Properties[collectionName]; ok && val != "" {
		meta.collectionName = val
	}

	if val, ok := metadata.Properties[writeConcern]; ok && val != "" {
		meta.writeconcern = val
	}

	if val, ok := metadata.Properties[readConcern]; ok && val != "" {
		meta.readconcern = val
	}

	var err error
	var t time.Duration
	if val, ok := metadata.Properties[operationTimeout]; ok && val != "" {
		t, err = time.ParseDuration(val)
	}

	if err != nil {
		return nil, errors.New("incorrect operationTimeout field from metadata")
	}

	meta.operationTimeout = t

	return &meta, nil
}

func getWriteConcernObject(cn string) (*writeconcern.WriteConcern, error) {
	var wc *writeconcern.WriteConcern
	if cn != "" {
		if cn == "majority" {
			wc = writeconcern.New(writeconcern.WMajority(), writeconcern.J(true), writeconcern.WTimeout(defaultTimeout))
		} else {
			w, err := strconv.Atoi(cn)
			wc = writeconcern.New(writeconcern.W(w), writeconcern.J(true), writeconcern.WTimeout(defaultTimeout))

			return wc, err
		}
	} else {
		wc = writeconcern.New(writeconcern.W(1), writeconcern.J(true), writeconcern.WTimeout(defaultTimeout))
	}

	return wc, nil
}

func getReadConcernObject(cn string) (*readconcern.ReadConcern, error) {
	switch cn {
	case "local":
		return readconcern.Local(), nil
	case "majority":
		return readconcern.Majority(), nil
	case "available":
		return readconcern.Available(), nil
	case "linearizable":
		return readconcern.Linearizable(), nil
	case "snapshot":
		return readconcern.Snapshot(), nil
	case "":
		return readconcern.Local(), nil
	}

	return nil, fmt.Errorf("readConcern %s not found", cn)
}
