// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson/bsoncodec"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	"go.mongodb.org/mongo-driver/x/mongo/driver"
	"go.mongodb.org/mongo-driver/x/mongo/driver/description"
	"go.mongodb.org/mongo-driver/x/mongo/driver/operation"
	"go.mongodb.org/mongo-driver/x/mongo/driver/session"
)

type bulkWriteBatch struct {
	models   []WriteModel
	canRetry bool
}

// bulkWrite perfoms a bulkwrite operation
type bulkWrite struct {
	ordered                  *bool
	bypassDocumentValidation *bool
	models                   []WriteModel
	session                  *session.Client
	collection               *Collection
	selector                 description.ServerSelector
	writeConcern             *writeconcern.WriteConcern
	result                   BulkWriteResult
}

func (bw *bulkWrite) execute(ctx context.Context) error {
	ordered := true
	if bw.ordered != nil {
		ordered = *bw.ordered
	}

	batches := createBatches(bw.models, ordered)
	bw.result = BulkWriteResult{
		UpsertedIDs: make(map[int64]interface{}),
	}

	bwErr := BulkWriteException{
		WriteErrors: make([]BulkWriteError, 0),
	}

	var lastErr error
	var opIndex int64 // the operation index for the upsertedIDs map
	continueOnError := !ordered
	for _, batch := range batches {
		if len(batch.models) == 0 {
			continue
		}

		bypassDocValidation := bw.bypassDocumentValidation
		if bypassDocValidation != nil && !*bypassDocValidation {
			bypassDocValidation = nil
		}

		batchRes, batchErr, err := bw.runBatch(ctx, batch)

		bw.mergeResults(batchRes, opIndex)

		bwErr.WriteConcernError = batchErr.WriteConcernError
		for i := range batchErr.WriteErrors {
			batchErr.WriteErrors[i].Index = batchErr.WriteErrors[i].Index + int(opIndex)
		}

		bwErr.WriteErrors = append(bwErr.WriteErrors, batchErr.WriteErrors...)

		if !continueOnError && (err != nil || len(batchErr.WriteErrors) > 0 || batchErr.WriteConcernError != nil) {
			if err != nil {
				return err
			}

			return bwErr
		}

		if err != nil {
			lastErr = err
		}

		opIndex += int64(len(batch.models))
	}

	bw.result.MatchedCount -= bw.result.UpsertedCount
	if lastErr != nil {
		return lastErr
	}
	if len(bwErr.WriteErrors) > 0 || bwErr.WriteConcernError != nil {
		return bwErr
	}
	return nil
}

func (bw *bulkWrite) runBatch(ctx context.Context, batch bulkWriteBatch) (BulkWriteResult, BulkWriteException, error) {
	batchRes := BulkWriteResult{
		UpsertedIDs: make(map[int64]interface{}),
	}
	batchErr := BulkWriteException{}

	var writeErrors []driver.WriteError
	switch batch.models[0].(type) {
	case *InsertOneModel:
		res, err := bw.runInsert(ctx, batch)
		if err != nil {
			writeErr, ok := err.(driver.WriteCommandError)
			if !ok {
				return BulkWriteResult{}, batchErr, err
			}
			writeErrors = writeErr.WriteErrors
			batchErr.WriteConcernError = convertDriverWriteConcernError(writeErr.WriteConcernError)
		}
		batchRes.InsertedCount = int64(res.N)
	case *DeleteOneModel, *DeleteManyModel:
		res, err := bw.runDelete(ctx, batch)
		if err != nil {
			writeErr, ok := err.(driver.WriteCommandError)
			if !ok {
				return BulkWriteResult{}, batchErr, err
			}
			writeErrors = writeErr.WriteErrors
			batchErr.WriteConcernError = convertDriverWriteConcernError(writeErr.WriteConcernError)
		}
		batchRes.DeletedCount = int64(res.N)
	case *ReplaceOneModel, *UpdateOneModel, *UpdateManyModel:
		res, err := bw.runUpdate(ctx, batch)
		if err != nil {
			writeErr, ok := err.(driver.WriteCommandError)
			if !ok {
				return BulkWriteResult{}, batchErr, err
			}
			writeErrors = writeErr.WriteErrors
			batchErr.WriteConcernError = convertDriverWriteConcernError(writeErr.WriteConcernError)
		}
		batchRes.MatchedCount = int64(res.N)
		batchRes.ModifiedCount = int64(res.NModified)
		batchRes.UpsertedCount = int64(len(res.Upserted))
		for _, upsert := range res.Upserted {
			batchRes.UpsertedIDs[upsert.Index] = upsert.ID
		}
	}

	batchErr.WriteErrors = make([]BulkWriteError, 0, len(writeErrors))
	convWriteErrors := writeErrorsFromDriverWriteErrors(writeErrors)
	for _, we := range convWriteErrors {
		batchErr.WriteErrors = append(batchErr.WriteErrors, BulkWriteError{
			WriteError: we,
			Request:    batch.models[0],
		})
	}
	return batchRes, batchErr, nil
}

func (bw *bulkWrite) runInsert(ctx context.Context, batch bulkWriteBatch) (operation.InsertResult, error) {
	docs := make([]bsoncore.Document, len(batch.models))
	var i int
	for _, model := range batch.models {
		converted := model.(*InsertOneModel)
		doc, _, err := transformAndEnsureIDv2(bw.collection.registry, converted.Document)
		if err != nil {
			return operation.InsertResult{}, err
		}

		docs[i] = doc
		i++
	}

	op := operation.NewInsert(docs...).
		Session(bw.session).WriteConcern(bw.writeConcern).CommandMonitor(bw.collection.client.monitor).
		ServerSelector(bw.selector).ClusterClock(bw.collection.client.clock).
		Database(bw.collection.db.name).Collection(bw.collection.name).
		Deployment(bw.collection.client.topology)
	if bw.bypassDocumentValidation != nil && *bw.bypassDocumentValidation {
		op = op.BypassDocumentValidation(*bw.bypassDocumentValidation)
	}
	if bw.ordered != nil {
		op = op.Ordered(*bw.ordered)
	}

	retry := driver.RetryNone
	if bw.collection.client.retryWrites && batch.canRetry {
		retry = driver.RetryOncePerCommand
	}
	op = op.Retry(retry)

	err := op.Execute(ctx)

	return op.Result(), err
}

func (bw *bulkWrite) runDelete(ctx context.Context, batch bulkWriteBatch) (operation.DeleteResult, error) {
	docs := make([]bsoncore.Document, len(batch.models))
	var i int

	for _, model := range batch.models {
		var doc bsoncore.Document
		var err error

		switch converted := model.(type) {
		case *DeleteOneModel:
			doc, err = createDeleteDoc(converted.Filter, converted.Collation, true, bw.collection.registry)
		case *DeleteManyModel:
			doc, err = createDeleteDoc(converted.Filter, converted.Collation, false, bw.collection.registry)
		}

		if err != nil {
			return operation.DeleteResult{}, err
		}

		docs[i] = doc
		i++
	}

	op := operation.NewDelete(docs...).
		Session(bw.session).WriteConcern(bw.writeConcern).CommandMonitor(bw.collection.client.monitor).
		ServerSelector(bw.selector).ClusterClock(bw.collection.client.clock).
		Database(bw.collection.db.name).Collection(bw.collection.name).
		Deployment(bw.collection.client.topology)
	if bw.ordered != nil {
		op = op.Ordered(*bw.ordered)
	}
	retry := driver.RetryNone
	if bw.collection.client.retryWrites && batch.canRetry {
		retry = driver.RetryOncePerCommand
	}
	op = op.Retry(retry)

	err := op.Execute(ctx)

	return op.Result(), err
}

func createDeleteDoc(filter interface{}, collation *options.Collation, deleteOne bool, registry *bsoncodec.Registry) (bsoncore.Document, error) {
	f, err := transformBsoncoreDocument(registry, filter)
	if err != nil {
		return nil, err
	}

	var limit int32
	if deleteOne {
		limit = 1
	}
	didx, doc := bsoncore.AppendDocumentStart(nil)
	doc = bsoncore.AppendDocumentElement(doc, "q", f)
	doc = bsoncore.AppendInt32Element(doc, "limit", limit)
	if collation != nil {
		doc = bsoncore.AppendDocumentElement(doc, "collation", collation.ToDocument())
	}
	doc, _ = bsoncore.AppendDocumentEnd(doc, didx)

	return doc, nil
}

func (bw *bulkWrite) runUpdate(ctx context.Context, batch bulkWriteBatch) (operation.UpdateResult, error) {
	docs := make([]bsoncore.Document, len(batch.models))
	for i, model := range batch.models {
		var doc bsoncore.Document
		var err error

		switch converted := model.(type) {
		case *ReplaceOneModel:
			doc, err = createUpdateDoc(converted.Filter, converted.Replacement, nil, converted.Collation, converted.Upsert, false,
				bw.collection.registry)
		case *UpdateOneModel:
			doc, err = createUpdateDoc(converted.Filter, converted.Update, converted.ArrayFilters, converted.Collation, converted.Upsert, false,
				bw.collection.registry)
		case *UpdateManyModel:
			doc, err = createUpdateDoc(converted.Filter, converted.Update, converted.ArrayFilters, converted.Collation, converted.Upsert, true,
				bw.collection.registry)
		}
		if err != nil {
			return operation.UpdateResult{}, err
		}

		docs[i] = doc
	}

	op := operation.NewUpdate(docs...).
		Session(bw.session).WriteConcern(bw.writeConcern).CommandMonitor(bw.collection.client.monitor).
		ServerSelector(bw.selector).ClusterClock(bw.collection.client.clock).
		Database(bw.collection.db.name).Collection(bw.collection.name).
		Deployment(bw.collection.client.topology)
	if bw.ordered != nil {
		op = op.Ordered(*bw.ordered)
	}
	if bw.bypassDocumentValidation != nil && *bw.bypassDocumentValidation {
		op = op.BypassDocumentValidation(*bw.bypassDocumentValidation)
	}
	retry := driver.RetryNone
	if bw.collection.client.retryWrites && batch.canRetry {
		retry = driver.RetryOncePerCommand
	}
	op = op.Retry(retry)

	err := op.Execute(ctx)

	return op.Result(), err
}
func createUpdateDoc(
	filter interface{},
	update interface{},
	arrayFilters *options.ArrayFilters,
	collation *options.Collation,
	upsert *bool,
	multi bool,
	registry *bsoncodec.Registry,
) (bsoncore.Document, error) {
	f, err := transformBsoncoreDocument(registry, filter)
	if err != nil {
		return nil, err
	}

	uidx, updateDoc := bsoncore.AppendDocumentStart(nil)
	updateDoc = bsoncore.AppendDocumentElement(updateDoc, "q", f)

	u, err := transformUpdateValue(registry, update, false)
	if err != nil {
		return nil, err
	}
	updateDoc = bsoncore.AppendValueElement(updateDoc, "u", u)

	updateDoc = bsoncore.AppendBooleanElement(updateDoc, "multi", multi)

	if arrayFilters != nil {
		arr, err := arrayFilters.ToArrayDocument()
		if err != nil {
			return nil, err
		}
		updateDoc = bsoncore.AppendArrayElement(updateDoc, "arrayFilters", arr)
	}

	if collation != nil {
		updateDoc = bsoncore.AppendDocumentElement(updateDoc, "collation", bsoncore.Document(collation.ToDocument()))
	}

	if upsert != nil {
		updateDoc = bsoncore.AppendBooleanElement(updateDoc, "upsert", *upsert)
	}
	updateDoc, _ = bsoncore.AppendDocumentEnd(updateDoc, uidx)

	return updateDoc, nil
}

func createBatches(models []WriteModel, ordered bool) []bulkWriteBatch {
	if ordered {
		return createOrderedBatches(models)
	}

	batches := make([]bulkWriteBatch, 5)
	batches[insertCommand].canRetry = true
	batches[deleteOneCommand].canRetry = true
	batches[updateOneCommand].canRetry = true

	// TODO(GODRIVER-1157): fix batching once operation retryability is fixed
	for _, model := range models {
		switch model.(type) {
		case *InsertOneModel:
			batches[insertCommand].models = append(batches[insertCommand].models, model)
		case *DeleteOneModel:
			batches[deleteOneCommand].models = append(batches[deleteOneCommand].models, model)
		case *DeleteManyModel:
			batches[deleteManyCommand].models = append(batches[deleteManyCommand].models, model)
		case *ReplaceOneModel, *UpdateOneModel:
			batches[updateOneCommand].models = append(batches[updateOneCommand].models, model)
		case *UpdateManyModel:
			batches[updateManyCommand].models = append(batches[updateManyCommand].models, model)
		}
	}

	return batches
}

func createOrderedBatches(models []WriteModel) []bulkWriteBatch {
	var batches []bulkWriteBatch
	var prevKind writeCommandKind = -1
	i := -1 // batch index

	for _, model := range models {
		var createNewBatch bool
		var canRetry bool
		var newKind writeCommandKind

		// TODO(GODRIVER-1157): fix batching once operation retryability is fixed
		switch model.(type) {
		case *InsertOneModel:
			createNewBatch = prevKind != insertCommand
			canRetry = true
			newKind = insertCommand
		case *DeleteOneModel:
			createNewBatch = prevKind != deleteOneCommand
			canRetry = true
			newKind = deleteOneCommand
		case *DeleteManyModel:
			createNewBatch = prevKind != deleteManyCommand
			newKind = deleteManyCommand
		case *ReplaceOneModel, *UpdateOneModel:
			createNewBatch = prevKind != updateOneCommand
			canRetry = true
			newKind = updateOneCommand
		case *UpdateManyModel:
			createNewBatch = prevKind != updateManyCommand
			newKind = updateManyCommand
		}

		if createNewBatch {
			batches = append(batches, bulkWriteBatch{
				models:   []WriteModel{model},
				canRetry: canRetry,
			})
			i++
		} else {
			batches[i].models = append(batches[i].models, model)
			if !canRetry {
				batches[i].canRetry = false // don't make it true if it was already false
			}
		}

		prevKind = newKind
	}

	return batches
}

func (bw *bulkWrite) mergeResults(newResult BulkWriteResult, opIndex int64) {
	bw.result.InsertedCount += newResult.InsertedCount
	bw.result.MatchedCount += newResult.MatchedCount
	bw.result.ModifiedCount += newResult.ModifiedCount
	bw.result.DeletedCount += newResult.DeletedCount
	bw.result.UpsertedCount += newResult.UpsertedCount

	for index, upsertID := range newResult.UpsertedIDs {
		bw.result.UpsertedIDs[index+opIndex] = upsertID
	}
}

// WriteCommandKind is the type of command represented by a Write
type writeCommandKind int8

// These constants represent the valid types of write commands.
const (
	insertCommand writeCommandKind = iota
	updateOneCommand
	updateManyCommand
	deleteOneCommand
	deleteManyCommand
)
