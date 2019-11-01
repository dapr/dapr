// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package mongo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsoncodec"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	"go.mongodb.org/mongo-driver/x/mongo/driver"
	"go.mongodb.org/mongo-driver/x/mongo/driver/description"
	"go.mongodb.org/mongo-driver/x/mongo/driver/operation"
	"go.mongodb.org/mongo-driver/x/mongo/driver/session"
)

// ErrInvalidIndexValue indicates that the index Keys document has a value that isn't either a number or a string.
var ErrInvalidIndexValue = errors.New("invalid index value")

// ErrNonStringIndexName indicates that the index name specified in the options is not a string.
var ErrNonStringIndexName = errors.New("index name must be a string")

// ErrMultipleIndexDrop indicates that multiple indexes would be dropped from a call to IndexView.DropOne.
var ErrMultipleIndexDrop = errors.New("multiple indexes would be dropped")

// IndexView is used to create, drop, and list indexes on a given collection.
type IndexView struct {
	coll *Collection
}

// IndexModel contains information about an index.
type IndexModel struct {
	Keys    interface{}
	Options *options.IndexOptions
}

func isNamespaceNotFoundError(err error) bool {
	if de, ok := err.(driver.Error); ok {
		return de.Code == 26
	}
	return false
}

// List returns a cursor iterating over all the indexes in the collection.
func (iv IndexView) List(ctx context.Context, opts ...*options.ListIndexesOptions) (*Cursor, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	sess := sessionFromContext(ctx)
	if sess == nil && iv.coll.client.topology.SessionPool != nil {
		var err error
		sess, err = session.NewClientSession(iv.coll.client.topology.SessionPool, iv.coll.client.id, session.Implicit)
		if err != nil {
			return nil, err
		}
	}

	err := iv.coll.client.validSession(sess)
	if err != nil {
		closeImplicitSession(sess)
		return nil, err
	}

	selector := description.CompositeSelector([]description.ServerSelector{
		description.ReadPrefSelector(readpref.Primary()),
		description.LatencySelector(iv.coll.client.localThreshold),
	})
	selector = makeReadPrefSelector(sess, selector, iv.coll.client.localThreshold)
	op := operation.NewListIndexes().
		Session(sess).CommandMonitor(iv.coll.client.monitor).
		ServerSelector(selector).ClusterClock(iv.coll.client.clock).
		Database(iv.coll.db.name).Collection(iv.coll.name).
		Deployment(iv.coll.client.topology)

	var cursorOpts driver.CursorOptions
	lio := options.MergeListIndexesOptions(opts...)
	if lio.BatchSize != nil {
		op = op.BatchSize(*lio.BatchSize)
		cursorOpts.BatchSize = *lio.BatchSize
	}
	if lio.MaxTime != nil {
		op = op.MaxTimeMS(int64(*lio.MaxTime / time.Millisecond))
	}
	retry := driver.RetryNone
	if iv.coll.client.retryReads {
		retry = driver.RetryOncePerCommand
	}
	op.Retry(retry)

	err = op.Execute(ctx)
	if err != nil {
		// for namespaceNotFound errors, return an empty cursor and do not throw an error
		closeImplicitSession(sess)
		if isNamespaceNotFoundError(err) {
			return newEmptyCursor(), nil
		}

		return nil, replaceErrors(err)
	}

	bc, err := op.Result(cursorOpts)
	if err != nil {
		closeImplicitSession(sess)
		return nil, replaceErrors(err)
	}
	cursor, err := newCursorWithSession(bc, iv.coll.registry, sess)
	return cursor, replaceErrors(err)
}

// CreateOne creates a single index in the collection specified by the model.
func (iv IndexView) CreateOne(ctx context.Context, model IndexModel, opts ...*options.CreateIndexesOptions) (string, error) {
	names, err := iv.CreateMany(ctx, []IndexModel{model}, opts...)
	if err != nil {
		return "", err
	}

	return names[0], nil
}

// CreateMany creates multiple indexes in the collection specified by the models. The names of the
// created indexes are returned.
func (iv IndexView) CreateMany(ctx context.Context, models []IndexModel, opts ...*options.CreateIndexesOptions) ([]string, error) {
	names := make([]string, 0, len(models))

	var indexes bsoncore.Document
	aidx, indexes := bsoncore.AppendArrayStart(indexes)

	for i, model := range models {
		if model.Keys == nil {
			return nil, fmt.Errorf("index model keys cannot be nil")
		}

		name, err := getOrGenerateIndexName(iv.coll.registry, model)
		if err != nil {
			return nil, err
		}

		names = append(names, name)

		keys, err := transformBsoncoreDocument(iv.coll.registry, model.Keys)
		if err != nil {
			return nil, err
		}

		var iidx int32
		iidx, indexes = bsoncore.AppendDocumentElementStart(indexes, strconv.Itoa(i))
		indexes = bsoncore.AppendDocumentElement(indexes, "key", keys)

		if model.Options == nil {
			model.Options = options.Index()
		}
		model.Options.SetName(name)

		optsDoc, err := iv.createOptionsDoc(model.Options)
		if err != nil {
			return nil, err
		}

		indexes = bsoncore.AppendDocument(indexes, optsDoc)

		indexes, err = bsoncore.AppendDocumentEnd(indexes, iidx)
		if err != nil {
			return nil, err
		}
	}

	indexes, err := bsoncore.AppendArrayEnd(indexes, aidx)
	if err != nil {
		return nil, err
	}

	sess := sessionFromContext(ctx)

	if sess == nil && iv.coll.client.topology.SessionPool != nil {
		sess, err = session.NewClientSession(iv.coll.client.topology.SessionPool, iv.coll.client.id, session.Implicit)
		if err != nil {
			return nil, err
		}
		defer sess.EndSession()
	}

	err = iv.coll.client.validSession(sess)
	if err != nil {
		return nil, err
	}

	selector := makePinnedSelector(sess, iv.coll.writeSelector)

	option := options.MergeCreateIndexesOptions(opts...)

	op := operation.NewCreateIndexes(indexes).
		Session(sess).ClusterClock(iv.coll.client.clock).
		Database(iv.coll.db.name).Collection(iv.coll.name).CommandMonitor(iv.coll.client.monitor).
		Deployment(iv.coll.client.topology).ServerSelector(selector)

	if option.MaxTime != nil {
		op.MaxTimeMS(int64(*option.MaxTime / time.Millisecond))
	}

	err = op.Execute(ctx)
	if err != nil {
		return nil, err
	}

	return names, nil
}

func (iv IndexView) createOptionsDoc(opts *options.IndexOptions) (bsoncore.Document, error) {
	optsDoc := bsoncore.Document{}
	if opts.Background != nil {
		optsDoc = bsoncore.AppendBooleanElement(optsDoc, "background", *opts.Background)
	}
	if opts.ExpireAfterSeconds != nil {
		optsDoc = bsoncore.AppendInt32Element(optsDoc, "expireAfterSeconds", *opts.ExpireAfterSeconds)
	}
	if opts.Name != nil {
		optsDoc = bsoncore.AppendStringElement(optsDoc, "name", *opts.Name)
	}
	if opts.Sparse != nil {
		optsDoc = bsoncore.AppendBooleanElement(optsDoc, "sparse", *opts.Sparse)
	}
	if opts.StorageEngine != nil {
		doc, err := transformBsoncoreDocument(iv.coll.registry, opts.StorageEngine)
		if err != nil {
			return nil, err
		}

		optsDoc = bsoncore.AppendDocumentElement(optsDoc, "storageEngine", doc)
	}
	if opts.Unique != nil {
		optsDoc = bsoncore.AppendBooleanElement(optsDoc, "unique", *opts.Unique)
	}
	if opts.Version != nil {
		optsDoc = bsoncore.AppendInt32Element(optsDoc, "v", *opts.Version)
	}
	if opts.DefaultLanguage != nil {
		optsDoc = bsoncore.AppendStringElement(optsDoc, "default_language", *opts.DefaultLanguage)
	}
	if opts.LanguageOverride != nil {
		optsDoc = bsoncore.AppendStringElement(optsDoc, "language_override", *opts.LanguageOverride)
	}
	if opts.TextVersion != nil {
		optsDoc = bsoncore.AppendInt32Element(optsDoc, "textIndexVersion", *opts.TextVersion)
	}
	if opts.Weights != nil {
		doc, err := transformBsoncoreDocument(iv.coll.registry, opts.Weights)
		if err != nil {
			return nil, err
		}

		optsDoc = bsoncore.AppendDocumentElement(optsDoc, "weights", doc)
	}
	if opts.SphereVersion != nil {
		optsDoc = bsoncore.AppendInt32Element(optsDoc, "2dsphereIndexVersion", *opts.SphereVersion)
	}
	if opts.Bits != nil {
		optsDoc = bsoncore.AppendInt32Element(optsDoc, "bits", *opts.Bits)
	}
	if opts.Max != nil {
		optsDoc = bsoncore.AppendDoubleElement(optsDoc, "max", *opts.Max)
	}
	if opts.Min != nil {
		optsDoc = bsoncore.AppendDoubleElement(optsDoc, "min", *opts.Min)
	}
	if opts.BucketSize != nil {
		optsDoc = bsoncore.AppendInt32Element(optsDoc, "bucketSize", *opts.BucketSize)
	}
	if opts.PartialFilterExpression != nil {
		doc, err := transformBsoncoreDocument(iv.coll.registry, opts.PartialFilterExpression)
		if err != nil {
			return nil, err
		}

		optsDoc = bsoncore.AppendDocumentElement(optsDoc, "partialFilterExpression", doc)
	}
	if opts.Collation != nil {
		optsDoc = bsoncore.AppendDocumentElement(optsDoc, "collation", bsoncore.Document(opts.Collation.ToDocument()))
	}
	if opts.WildcardProjection != nil {
		doc, err := transformBsoncoreDocument(iv.coll.registry, opts.WildcardProjection)
		if err != nil {
			return nil, err
		}

		optsDoc = bsoncore.AppendDocumentElement(optsDoc, "wildcardProjection", doc)
	}

	return optsDoc, nil
}

func (iv IndexView) drop(ctx context.Context, name string, opts ...*options.DropIndexesOptions) (bson.Raw, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	sess := sessionFromContext(ctx)
	if sess == nil && iv.coll.client.topology.SessionPool != nil {
		var err error
		sess, err = session.NewClientSession(iv.coll.client.topology.SessionPool, iv.coll.client.id, session.Implicit)
		if err != nil {
			return nil, err
		}
		defer sess.EndSession()
	}

	err := iv.coll.client.validSession(sess)
	if err != nil {
		return nil, err
	}

	wc := iv.coll.writeConcern
	if sess.TransactionRunning() {
		wc = nil
	}
	if !writeconcern.AckWrite(wc) {
		sess = nil
	}

	selector := makePinnedSelector(sess, iv.coll.writeSelector)

	dio := options.MergeDropIndexesOptions(opts...)
	op := operation.NewDropIndexes(name).
		Session(sess).WriteConcern(wc).CommandMonitor(iv.coll.client.monitor).
		ServerSelector(selector).ClusterClock(iv.coll.client.clock).
		Database(iv.coll.db.name).Collection(iv.coll.name).
		Deployment(iv.coll.client.topology)
	if dio.MaxTime != nil {
		op.MaxTimeMS(int64(*dio.MaxTime / time.Millisecond))
	}

	err = op.Execute(ctx)
	if err != nil {
		return nil, replaceErrors(err)
	}

	// TODO: it's weird to return a bson.Raw here because we have to convert the result back to BSON
	ridx, res := bsoncore.AppendDocumentStart(nil)
	res = bsoncore.AppendInt32Element(res, "nIndexesWas", op.Result().NIndexesWas)
	res, _ = bsoncore.AppendDocumentEnd(res, ridx)
	return res, nil
}

// DropOne drops the index with the given name from the collection.
func (iv IndexView) DropOne(ctx context.Context, name string, opts ...*options.DropIndexesOptions) (bson.Raw, error) {
	if name == "*" {
		return nil, ErrMultipleIndexDrop
	}

	return iv.drop(ctx, name, opts...)
}

// DropAll drops all indexes in the collection.
func (iv IndexView) DropAll(ctx context.Context, opts ...*options.DropIndexesOptions) (bson.Raw, error) {
	return iv.drop(ctx, "*", opts...)
}

func getOrGenerateIndexName(registry *bsoncodec.Registry, model IndexModel) (string, error) {
	if model.Options != nil && model.Options.Name != nil {
		return *model.Options.Name, nil
	}

	name := bytes.NewBufferString("")
	first := true

	keys, err := transformDocument(registry, model.Keys)
	if err != nil {
		return "", err
	}
	for _, elem := range keys {
		if !first {
			_, err := name.WriteRune('_')
			if err != nil {
				return "", err
			}
		}

		_, err := name.WriteString(elem.Key)
		if err != nil {
			return "", err
		}

		_, err = name.WriteRune('_')
		if err != nil {
			return "", err
		}

		var value string

		switch elem.Value.Type() {
		case bsontype.Int32:
			value = fmt.Sprintf("%d", elem.Value.Int32())
		case bsontype.Int64:
			value = fmt.Sprintf("%d", elem.Value.Int64())
		case bsontype.String:
			value = elem.Value.StringValue()
		default:
			return "", ErrInvalidIndexValue
		}

		_, err = name.WriteString(value)
		if err != nil {
			return "", err
		}

		first = false
	}

	return name.String(), nil
}
