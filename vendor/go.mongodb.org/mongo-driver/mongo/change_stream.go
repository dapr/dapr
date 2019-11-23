// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package mongo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsoncodec"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	"go.mongodb.org/mongo-driver/x/mongo/driver"
	"go.mongodb.org/mongo-driver/x/mongo/driver/description"
	"go.mongodb.org/mongo-driver/x/mongo/driver/operation"
	"go.mongodb.org/mongo-driver/x/mongo/driver/session"
)

const errorInterrupted int32 = 11601
const errorCappedPositionLost int32 = 136
const errorCursorKilled int32 = 237

// ErrMissingResumeToken indicates that a change stream notification from the server did not
// contain a resume token.
var ErrMissingResumeToken = errors.New("cannot provide resume functionality when the resume token is missing")

// ErrNilCursor indicates that the cursor for the change stream is nil.
var ErrNilCursor = errors.New("cursor is nil")

// ChangeStream instances iterate a stream of change documents. Each document can be decoded via the
// Decode method. Resume tokens should be retrieved via the ResumeToken method and can be stored to
// resume the change stream at a specific point in time.
//
// A typical usage of the ChangeStream type would be:
type ChangeStream struct {
	Current bson.Raw

	aggregate     *operation.Aggregate
	pipelineSlice []bsoncore.Document
	cursor        changeStreamCursor
	cursorOptions driver.CursorOptions
	batch         []bsoncore.Document
	resumeToken   bson.Raw
	err           error
	sess          *session.Client
	client        *Client
	registry      *bsoncodec.Registry
	streamType    StreamType
	options       *options.ChangeStreamOptions
	selector      description.ServerSelector
	operationTime *primitive.Timestamp
}

type changeStreamConfig struct {
	readConcern    *readconcern.ReadConcern
	readPreference *readpref.ReadPref
	client         *Client
	registry       *bsoncodec.Registry
	streamType     StreamType
	collectionName string
	databaseName   string
}

func newChangeStream(ctx context.Context, config changeStreamConfig, pipeline interface{},
	opts ...*options.ChangeStreamOptions) (*ChangeStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cs := &ChangeStream{
		client:     config.client,
		registry:   config.registry,
		streamType: config.streamType,
		options:    options.MergeChangeStreamOptions(opts...),
		selector:   description.ReadPrefSelector(config.readPreference),
	}

	cs.sess = sessionFromContext(ctx)
	if cs.sess == nil && cs.client.topology.SessionPool != nil {
		cs.sess, cs.err = session.NewClientSession(cs.client.topology.SessionPool, cs.client.id, session.Implicit)
		if cs.err != nil {
			return nil, cs.Err()
		}
	}
	if cs.err = cs.client.validSession(cs.sess); cs.err != nil {
		closeImplicitSession(cs.sess)
		return nil, cs.Err()
	}

	cs.aggregate = operation.NewAggregate(nil).
		ReadPreference(config.readPreference).ReadConcern(config.readConcern).
		Deployment(cs.client.topology).ClusterClock(cs.client.clock).
		CommandMonitor(cs.client.monitor).Session(cs.sess).ServerSelector(cs.selector).Retry(driver.RetryNone)

	if cs.options.Collation != nil {
		cs.aggregate.Collation(bsoncore.Document(cs.options.Collation.ToDocument()))
	}
	if cs.options.BatchSize != nil {
		cs.aggregate.BatchSize(*cs.options.BatchSize)
		cs.cursorOptions.BatchSize = *cs.options.BatchSize
	}
	if cs.options.MaxAwaitTime != nil {
		cs.cursorOptions.MaxTimeMS = int64(time.Duration(*cs.options.MaxAwaitTime) / time.Millisecond)
	}
	cs.cursorOptions.CommandMonitor = cs.client.monitor

	switch cs.streamType {
	case ClientStream:
		cs.aggregate.Database("admin")
	case DatabaseStream:
		cs.aggregate.Database(config.databaseName)
	case CollectionStream:
		cs.aggregate.Collection(config.collectionName).Database(config.databaseName)
	default:
		closeImplicitSession(cs.sess)
		return nil, fmt.Errorf("must supply a valid StreamType in config, instead of %v", cs.streamType)
	}

	// When starting a change stream, cache startAfter as the first resume token if it is set. If not, cache
	// resumeAfter. If neither is set, do not cache a resume token.
	resumeToken := cs.options.StartAfter
	if resumeToken == nil {
		resumeToken = cs.options.ResumeAfter
	}
	var marshaledToken bson.Raw
	if resumeToken != nil {
		if marshaledToken, cs.err = bson.Marshal(resumeToken); cs.err != nil {
			closeImplicitSession(cs.sess)
			return nil, cs.Err()
		}
	}
	cs.resumeToken = marshaledToken

	if cs.err = cs.buildPipelineSlice(pipeline); cs.err != nil {
		closeImplicitSession(cs.sess)
		return nil, cs.Err()
	}
	var pipelineArr bsoncore.Document
	pipelineArr, cs.err = cs.pipelineToBSON()
	cs.aggregate.Pipeline(pipelineArr)

	if cs.err = cs.executeOperation(ctx, false); cs.err != nil {
		closeImplicitSession(cs.sess)
		return nil, cs.Err()
	}

	return cs, cs.Err()
}

func (cs *ChangeStream) executeOperation(ctx context.Context, resuming bool) error {
	var server driver.Server
	var conn driver.Connection
	var err error

	if server, cs.err = cs.client.topology.SelectServer(ctx, cs.selector); cs.err != nil {
		return cs.Err()
	}
	if conn, cs.err = server.Connection(ctx); cs.err != nil {
		return cs.Err()
	}

	defer conn.Close()

	cs.aggregate.Deployment(driver.SingleConnectionDeployment{
		C: conn,
	})

	if resuming {
		cs.replaceOptions(ctx, conn.Description().WireVersion) // pass wire version

		csOptDoc := cs.createPipelineOptionsDoc()
		pipIdx, pipDoc := bsoncore.AppendDocumentStart(nil)
		pipDoc = bsoncore.AppendDocumentElement(pipDoc, "$changeStream", csOptDoc)
		if pipDoc, cs.err = bsoncore.AppendDocumentEnd(pipDoc, pipIdx); cs.err != nil {
			return cs.Err()
		}
		cs.pipelineSlice[0] = pipDoc

		var plArr bsoncore.Document
		if plArr, cs.err = cs.pipelineToBSON(); cs.err != nil {
			return cs.Err()
		}
		cs.aggregate.Pipeline(plArr)
	}

	if original := cs.aggregate.Execute(ctx); original != nil {
		wireVersion := conn.Description().WireVersion
		retryableRead := cs.client.retryReads && wireVersion != nil && wireVersion.Max >= 6
		if !retryableRead {
			cs.err = replaceErrors(original)
			return cs.err
		}

		cs.err = original
		switch tt := original.(type) {
		case driver.Error:
			if !tt.Retryable() {
				break
			}

			server, err = cs.client.topology.SelectServer(ctx, cs.selector)
			if err != nil {
				break
			}

			conn.Close()
			conn, err = server.Connection(ctx)
			defer conn.Close()

			if err != nil {
				break
			}

			wireVersion := conn.Description().WireVersion
			if wireVersion == nil || wireVersion.Max < 6 {
				break
			}

			cs.aggregate.Deployment(driver.SingleConnectionDeployment{
				C: conn,
			})
			cs.err = cs.aggregate.Execute(ctx)
		}

		if cs.err != nil {
			cs.err = replaceErrors(cs.err)
			return cs.Err()
		}

	}
	cs.err = nil

	cr := cs.aggregate.ResultCursorResponse()
	cr.Server = server

	cs.cursor, cs.err = driver.NewBatchCursor(cr, cs.sess, cs.client.clock, cs.cursorOptions)
	if cs.err = replaceErrors(cs.err); cs.err != nil {
		return cs.Err()
	}

	cs.updatePbrtFromCommand()
	if cs.options.StartAtOperationTime == nil && cs.options.ResumeAfter == nil &&
		cs.options.StartAfter == nil && conn.Description().WireVersion.Max >= 7 &&
		cs.emptyBatch() && cs.resumeToken == nil {
		cs.operationTime = cs.sess.OperationTime
	}

	return cs.Err()
}

// Updates the post batch resume token after a successful aggregate or getMore operation.
func (cs *ChangeStream) updatePbrtFromCommand() {
	// Only cache the pbrt if an empty batch was returned and a pbrt was included
	if pbrt := cs.cursor.PostBatchResumeToken(); cs.emptyBatch() && pbrt != nil {
		cs.resumeToken = bson.Raw(pbrt)
	}
}

func (cs *ChangeStream) storeResumeToken() error {
	// If cs.Current is the last document in the batch and a pbrt is included, cache the pbrt
	// Otherwise, cache the _id of the document
	var tokenDoc bson.Raw
	if len(cs.batch) == 0 {
		if pbrt := cs.cursor.PostBatchResumeToken(); pbrt != nil {
			tokenDoc = bson.Raw(pbrt)
		}
	}

	if tokenDoc == nil {
		var ok bool
		tokenDoc, ok = cs.Current.Lookup("_id").DocumentOK()
		if !ok {
			_ = cs.Close(context.Background())
			return ErrMissingResumeToken
		}
	}

	cs.resumeToken = tokenDoc
	return nil
}

func (cs *ChangeStream) buildPipelineSlice(pipeline interface{}) error {
	val := reflect.ValueOf(pipeline)
	if !val.IsValid() || !(val.Kind() == reflect.Slice) {
		cs.err = errors.New("can only transform slices and arrays into aggregation pipelines, but got invalid")
		return cs.err
	}

	cs.pipelineSlice = make([]bsoncore.Document, 0, val.Len()+1)

	csIdx, csDoc := bsoncore.AppendDocumentStart(nil)
	csDocTemp := cs.createPipelineOptionsDoc()
	if cs.err != nil {
		return cs.err
	}
	csDoc = bsoncore.AppendDocumentElement(csDoc, "$changeStream", csDocTemp)
	csDoc, cs.err = bsoncore.AppendDocumentEnd(csDoc, csIdx)
	if cs.err != nil {
		return cs.err
	}
	cs.pipelineSlice = append(cs.pipelineSlice, csDoc)

	for i := 0; i < val.Len(); i++ {
		var elem []byte
		elem, cs.err = transformBsoncoreDocument(cs.registry, val.Index(i).Interface())
		if cs.err != nil {
			return cs.err
		}

		cs.pipelineSlice = append(cs.pipelineSlice, elem)
	}

	return cs.err
}

func (cs *ChangeStream) createPipelineOptionsDoc() bsoncore.Document {
	plDocIdx, plDoc := bsoncore.AppendDocumentStart(nil)

	if cs.streamType == ClientStream {
		plDoc = bsoncore.AppendBooleanElement(plDoc, "allChangesForCluster", true)
	}

	if cs.options.FullDocument != nil {
		plDoc = bsoncore.AppendStringElement(plDoc, "fullDocument", string(*cs.options.FullDocument))
	}

	if cs.options.ResumeAfter != nil {
		var raDoc bsoncore.Document
		raDoc, cs.err = transformBsoncoreDocument(cs.registry, cs.options.ResumeAfter)
		if cs.err != nil {
			return nil
		}

		plDoc = bsoncore.AppendDocumentElement(plDoc, "resumeAfter", raDoc)
	}

	if cs.options.StartAfter != nil {
		var saDoc bsoncore.Document
		saDoc, cs.err = transformBsoncoreDocument(cs.registry, cs.options.StartAfter)
		if cs.err != nil {
			return nil
		}

		plDoc = bsoncore.AppendDocumentElement(plDoc, "startAfter", saDoc)
	}

	if cs.options.StartAtOperationTime != nil {
		plDoc = bsoncore.AppendTimestampElement(plDoc, "startAtOperationTime", cs.options.StartAtOperationTime.T, cs.options.StartAtOperationTime.I)
	}

	if plDoc, cs.err = bsoncore.AppendDocumentEnd(plDoc, plDocIdx); cs.err != nil {
		return nil
	}

	return plDoc
}

func (cs *ChangeStream) pipelineToBSON() (bsoncore.Document, error) {
	pipelineDocIdx, pipelineArr := bsoncore.AppendArrayStart(nil)
	for i, doc := range cs.pipelineSlice {
		pipelineArr = bsoncore.AppendDocumentElement(pipelineArr, strconv.Itoa(i), doc)
	}
	if pipelineArr, cs.err = bsoncore.AppendArrayEnd(pipelineArr, pipelineDocIdx); cs.err != nil {
		return nil, cs.err
	}
	return pipelineArr, cs.err
}

func (cs *ChangeStream) replaceOptions(ctx context.Context, wireVersion *description.VersionRange) {
	// Cached resume token: use the resume token as the resumeAfter option and set no other resume options
	if cs.resumeToken != nil {
		cs.options.SetResumeAfter(cs.resumeToken)
		cs.options.SetStartAfter(nil)
		cs.options.SetStartAtOperationTime(nil)
		return
	}

	// No cached resume token but cached operation time: use the operation time as the startAtOperationTime option and
	// set no other resume options
	if (cs.sess.OperationTime != nil || cs.options.StartAtOperationTime != nil) && wireVersion.Max >= 7 {
		opTime := cs.options.StartAtOperationTime
		if cs.operationTime != nil {
			opTime = cs.sess.OperationTime
		}

		cs.options.SetStartAtOperationTime(opTime)
		cs.options.SetResumeAfter(nil)
		cs.options.SetStartAfter(nil)
		return
	}

	// No cached resume token or operation time: set none of the resume options
	cs.options.SetResumeAfter(nil)
	cs.options.SetStartAfter(nil)
	cs.options.SetStartAtOperationTime(nil)
}

// ID returns the cursor ID for this change stream.
func (cs *ChangeStream) ID() int64 {
	if cs.cursor == nil {
		return 0
	}
	return cs.cursor.ID()
}

// Decode will decode the current document into val.
func (cs *ChangeStream) Decode(val interface{}) error {
	if cs.cursor == nil {
		return ErrNilCursor
	}

	return bson.UnmarshalWithRegistry(cs.registry, cs.Current, val)
}

// Err returns the current error.
func (cs *ChangeStream) Err() error {
	if cs.err != nil {
		return replaceErrors(cs.err)
	}
	if cs.cursor == nil {
		return nil
	}

	return replaceErrors(cs.cursor.Err())
}

// Close closes this cursor.
func (cs *ChangeStream) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	defer closeImplicitSession(cs.sess)

	if cs.cursor == nil {
		return nil // cursor is already closed
	}

	cs.err = replaceErrors(cs.cursor.Close(ctx))
	cs.cursor = nil
	return cs.Err()
}

// ResumeToken returns the last cached resume token for this change stream.
func (cs *ChangeStream) ResumeToken() bson.Raw {
	return cs.resumeToken
}

// Next gets the next result from this change stream. Returns true if there were no errors and the next
// result is available for decoding.
func (cs *ChangeStream) Next(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}

	if len(cs.batch) == 0 {
		cs.loopNext(ctx)
		if cs.err != nil || len(cs.batch) == 0 {
			cs.err = replaceErrors(cs.err)
			return false
		}
	}

	cs.Current = bson.Raw(cs.batch[0])
	cs.batch = cs.batch[1:]
	if cs.err = cs.storeResumeToken(); cs.err != nil {
		return false
	}
	return true
}

func (cs *ChangeStream) loopNext(ctx context.Context) {
	for {
		if cs.cursor == nil {
			return
		}

		if cs.cursor.Next(ctx) {
			// If this is the first batch, the batch cursor will return true, but the batch could be empty.
			if cs.batch, cs.err = cs.cursor.Batch().Documents(); cs.err != nil || len(cs.batch) > 0 {
				return
			}

			// no error but empty batch
			cs.updatePbrtFromCommand()
			continue
		}

		cs.err = replaceErrors(cs.cursor.Err())
		if cs.err == nil {
			// If a getMore was done but the batch was empty, the batch cursor will return false with no error
			if len(cs.batch) == 0 {
				continue
			}
			return
		}

		switch t := cs.err.(type) {
		case CommandError:
			if t.Code == errorInterrupted || t.Code == errorCappedPositionLost || t.Code == errorCursorKilled || t.HasErrorLabel("NonResumableChangeStreamError") {
				return
			}
		}

		// ignore error from cursor close because if the cursor is deleted or errors we tried to close it and will remake and try to get next batch
		_ = cs.cursor.Close(ctx)
		if cs.err = cs.executeOperation(ctx, true); cs.err != nil {
			return
		}
	}
}

// Returns true if the underlying cursor's batch is empty
func (cs *ChangeStream) emptyBatch() bool {
	return cs.cursor.Batch().Empty()
}

// StreamType represents the type of a change stream.
type StreamType uint8

// These constants represent valid change stream types. A change stream can be initialized over a collection, all
// collections in a database, or over a whole client.
const (
	CollectionStream StreamType = iota
	DatabaseStream
	ClientStream
)
