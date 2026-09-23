/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package universal

import (
	"context"

	compsearch "github.com/dapr/components-contrib/search"
	compvector "github.com/dapr/components-contrib/vector"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

func (a *Universal) CreateCollectionAlpha1(ctx context.Context, req *runtimev1pb.CreateCollectionRequestAlpha1) (*emptypb.Empty, error) {
	storeName := req.GetStoreName()
	comp, err := a.getVectorStore(storeName)
	if err != nil {
		return nil, err
	}
	if req.GetDimensions() == 0 {
		return nil, vectorInvalidRequest(storeName, newFieldError("dimensions", "dimensions must be greater than zero"))
	}

	compReq := &compvector.CreateCollectionRequest{
		Collection: req.GetCollection(),
		Metadata:   req.GetMetadata(),
		Dimensions: req.GetDimensions(),
		Metric:     compvector.DistanceMetric(req.GetMetric()),
	}
	err = runVectorPolicy(ctx, a, storeName, func(ctx context.Context) error {
		return comp.CreateCollection(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (a *Universal) GetCollectionAlpha1(ctx context.Context, req *runtimev1pb.GetCollectionRequestAlpha1) (*runtimev1pb.GetCollectionResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getVectorStore(storeName)
	if err != nil {
		return nil, err
	}

	compReq := &compvector.GetCollectionRequest{
		Collection: req.GetCollection(),
		Metadata:   req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compvector.GetCollectionResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compvector.GetCollectionResponse, error) {
		return comp.GetCollection(ctx, compReq)
	})
	if err != nil {
		return nil, componentError(err)
	}
	if resp == nil {
		return &runtimev1pb.GetCollectionResponseAlpha1{}, nil
	}
	return &runtimev1pb.GetCollectionResponseAlpha1{
		Collection:  resp.Collection,
		RecordCount: resp.RecordCount,
		Properties:  resp.Properties,
		Dimensions:  resp.Dimensions,
		Metric:      runtimev1pb.DistanceMetric(resp.Metric),
	}, nil
}

func (a *Universal) ListCollectionsAlpha1(ctx context.Context, req *runtimev1pb.ListCollectionsRequestAlpha1) (*runtimev1pb.ListCollectionsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getVectorStore(storeName)
	if err != nil {
		return nil, err
	}

	compReq := &compvector.ListCollectionsRequest{Metadata: req.GetMetadata()}
	policyRunner := resiliency.NewRunner[*compvector.ListCollectionsResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compvector.ListCollectionsResponse, error) {
		return comp.ListCollections(ctx, compReq)
	})
	if err != nil {
		return nil, componentError(err)
	}
	if resp == nil {
		return &runtimev1pb.ListCollectionsResponseAlpha1{}, nil
	}
	return &runtimev1pb.ListCollectionsResponseAlpha1{Collections: resp.Collections}, nil
}

func (a *Universal) DeleteCollectionAlpha1(ctx context.Context, req *runtimev1pb.DeleteCollectionRequestAlpha1) (*emptypb.Empty, error) {
	storeName := req.GetStoreName()
	comp, err := a.getVectorStore(storeName)
	if err != nil {
		return nil, err
	}

	compReq := &compvector.DeleteCollectionRequest{
		Collection: req.GetCollection(),
		Metadata:   req.GetMetadata(),
	}
	err = runVectorPolicy(ctx, a, storeName, func(ctx context.Context) error {
		return comp.DeleteCollection(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// UpsertVectorsAlpha1 is a keyed upsert: every record carries a non-empty,
// caller-supplied ID that is unique within the request.
func (a *Universal) UpsertVectorsAlpha1(ctx context.Context, req *runtimev1pb.UpsertVectorsRequestAlpha1) (*runtimev1pb.UpsertVectorsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getVectorStore(storeName)
	if err != nil {
		return nil, err
	}

	records := make([]compvector.Record, 0, len(req.GetRecords()))
	ids := make([]string, 0, len(req.GetRecords()))
	for _, record := range req.GetRecords() {
		ids = append(ids, record.GetId())
		records = append(records, protoVectorRecordToComponent(record))
	}
	if err := compsearch.ValidateWriteIDs(ids); err != nil {
		return nil, vectorInvalidRequest(storeName, fieldErrorFrom("records.id", err))
	}

	options, err := protoIndexingOptionsToComponent(ctx, req.GetOptions())
	if err != nil {
		return nil, vectorInvalidRequest(storeName, err)
	}

	compReq := &compvector.UpsertRequest{
		Collection: req.GetCollection(),
		Records:    records,
		Metadata:   req.GetMetadata(),
		Options:    options,
	}
	policyRunner := resiliency.NewRunner[*compvector.UpsertResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compvector.UpsertResponse, error) {
		return comp.Upsert(ctx, compReq)
	})
	if err != nil {
		return nil, componentError(err)
	}
	if resp == nil {
		return &runtimev1pb.UpsertVectorsResponseAlpha1{Ack: runtimev1pb.IndexAck_INDEX_ACK_COMPLETED}, nil
	}
	return &runtimev1pb.UpsertVectorsResponseAlpha1{
		FailedItems: componentFailedItemsToProto(resp.FailedItems),
		Ack:         componentIndexAckToProto(resp.Ack),
	}, nil
}

func (a *Universal) GetVectorsAlpha1(ctx context.Context, req *runtimev1pb.GetVectorsRequestAlpha1) (*runtimev1pb.GetVectorsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getVectorStore(storeName)
	if err != nil {
		return nil, err
	}

	compReq := &compvector.GetRequest{
		Collection:    req.GetCollection(),
		IDs:           req.GetIds(),
		IncludeValues: req.GetIncludeValues(),
		Metadata:      req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compvector.GetResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compvector.GetResponse, error) {
		return comp.Get(ctx, compReq)
	})
	if err != nil {
		return nil, componentError(err)
	}
	if resp == nil {
		return &runtimev1pb.GetVectorsResponseAlpha1{}, nil
	}
	records, err := componentVectorRecordsToProto(resp.Records)
	if err != nil {
		return nil, err
	}
	return &runtimev1pb.GetVectorsResponseAlpha1{Records: records}, nil
}

// DeleteVectorsAlpha1 is a write and shares the acknowledgement model of
// UpsertVectorsAlpha1. IDs that do not exist are not an error.
func (a *Universal) DeleteVectorsAlpha1(ctx context.Context, req *runtimev1pb.DeleteVectorsRequestAlpha1) (*runtimev1pb.DeleteVectorsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getVectorStore(storeName)
	if err != nil {
		return nil, err
	}

	options, err := protoIndexingOptionsToComponent(ctx, req.GetOptions())
	if err != nil {
		return nil, vectorInvalidRequest(storeName, err)
	}

	compReq := &compvector.DeleteRequest{
		Collection: req.GetCollection(),
		IDs:        req.GetIds(),
		Metadata:   req.GetMetadata(),
		Options:    options,
	}
	policyRunner := resiliency.NewRunner[*compvector.DeleteResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compvector.DeleteResponse, error) {
		return comp.Delete(ctx, compReq)
	})
	if err != nil {
		return nil, componentError(err)
	}
	if resp == nil {
		return &runtimev1pb.DeleteVectorsResponseAlpha1{Ack: runtimev1pb.IndexAck_INDEX_ACK_COMPLETED}, nil
	}
	return &runtimev1pb.DeleteVectorsResponseAlpha1{Ack: componentIndexAckToProto(resp.Ack)}, nil
}

func (a *Universal) QueryVectorsAlpha1(ctx context.Context, req *runtimev1pb.QueryVectorsRequestAlpha1) (*runtimev1pb.QueryVectorsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getVectorStore(storeName)
	if err != nil {
		return nil, err
	}

	compReq, err := protoQueryVectorsRequestToComponent(req)
	if err != nil {
		return nil, vectorInvalidRequest(storeName, err)
	}
	policyRunner := resiliency.NewRunner[*compvector.QueryResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compvector.QueryResponse, error) {
		return comp.Query(ctx, compReq)
	})
	if err != nil {
		return nil, componentError(err)
	}
	return componentQueryVectorsResponseToProto(resp)
}

// BatchQueryVectorsAlpha1 evaluates each query independently. A query that
// fails validation or is rejected by the provider yields an error result in
// its slot; only request-wide failures fail the RPC.
func (a *Universal) BatchQueryVectorsAlpha1(ctx context.Context, req *runtimev1pb.BatchQueryVectorsRequestAlpha1) (*runtimev1pb.BatchQueryVectorsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getVectorStore(storeName)
	if err != nil {
		return nil, err
	}

	// Queries that fail runtime validation are settled here and never sent to
	// the component; the remaining queries keep their request-order index.
	results := make([]*runtimev1pb.BatchQueryResultAlpha1, len(req.GetQueries()))
	queries := make([]compvector.QueryRequest, 0, len(req.GetQueries()))
	positions := make([]int, 0, len(req.GetQueries()))
	for i, query := range req.GetQueries() {
		compQuery, err := protoQueryVectorsRequestToComponent(query)
		if err != nil {
			results[i] = &runtimev1pb.BatchQueryResultAlpha1{
				Result: &runtimev1pb.BatchQueryResultAlpha1_Error{
					Error: grpcstatus.Convert(vectorInvalidRequest(storeName, err)).Proto(),
				},
			}
			continue
		}
		compQuery.Collection = req.GetCollection()
		queries = append(queries, *compQuery)
		positions = append(positions, i)
	}

	if len(queries) > 0 {
		compReq := &compvector.BatchQueryRequest{
			Collection: req.GetCollection(),
			Queries:    queries,
			Metadata:   req.GetMetadata(),
		}
		policyRunner := resiliency.NewRunner[*compvector.BatchQueryResponse](ctx,
			a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
		)
		resp, err := policyRunner(func(ctx context.Context) (*compvector.BatchQueryResponse, error) {
			return comp.BatchQuery(ctx, compReq)
		})
		if err != nil {
			return nil, componentError(err)
		}
		if resp == nil || len(resp.Results) != len(queries) {
			return nil, grpcstatus.Errorf(codes.Internal, "vector store %s returned %d batch results for %d queries", storeName, batchResultCount(resp), len(queries))
		}
		for j := range resp.Results {
			results[positions[j]] = componentBatchQueryResultToProto(&resp.Results[j])
		}
	}

	return &runtimev1pb.BatchQueryVectorsResponseAlpha1{Results: results}, nil
}

func batchResultCount(resp *compvector.BatchQueryResponse) int {
	if resp == nil {
		return 0
	}
	return len(resp.Results)
}

func runVectorPolicy(ctx context.Context, a *Universal, storeName string, fn func(ctx context.Context) error) error {
	policyRunner := resiliency.NewRunner[struct{}](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return componentError(err)
}

func protoVectorRecordToComponent(record *runtimev1pb.VectorRecord) compvector.Record {
	return compvector.Record{
		ID:       record.GetId(),
		Values:   record.GetValues(),
		Payload:  record.GetPayload(),
		Metadata: protoStructToMap(record.GetMetadata()),
	}
}

func componentVectorRecordToProto(record compvector.Record) (*runtimev1pb.VectorRecord, error) {
	out := &runtimev1pb.VectorRecord{
		Id:      record.ID,
		Values:  record.Values,
		Payload: record.Payload,
	}
	if record.Metadata != nil {
		metadata, err := structpb.NewStruct(record.Metadata)
		if err != nil {
			return nil, grpcstatus.Errorf(codes.Internal, "vector record %q metadata is not representable as a JSON object: %s", record.ID, err)
		}
		out.Metadata = metadata
	}
	return out, nil
}

func componentVectorRecordsToProto(records []compvector.Record) ([]*runtimev1pb.VectorRecord, error) {
	if len(records) == 0 {
		return nil, nil
	}

	out := make([]*runtimev1pb.VectorRecord, 0, len(records))
	for _, record := range records {
		converted, err := componentVectorRecordToProto(record)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func protoQueryVectorsRequestToComponent(req *runtimev1pb.QueryVectorsRequestAlpha1) (*compvector.QueryRequest, error) {
	out := &compvector.QueryRequest{
		Collection:     req.GetCollection(),
		ByID:           req.GetById(),
		TopK:           req.GetTopK(),
		Filter:         protoStructToMap(req.GetFilter()),
		IncludeValues:  req.GetIncludeValues(),
		IncludePayload: req.GetIncludePayload(),
		Metric:         compvector.DistanceMetric(req.GetMetric()),
		ScoreThreshold: req.ScoreThreshold,
		Metadata:       req.GetMetadata(),
	}
	if vec := req.GetVector(); vec != nil {
		// Only the query values are meaningful.
		out.Vector = &compvector.Record{Values: vec.GetValues()}
	}
	if (out.Vector == nil) == (out.ByID == "") {
		return nil, newFieldError("query", "exactly one of vector or by_id must be set")
	}
	return out, nil
}

func componentQueryVectorsResponseToProto(resp *compvector.QueryResponse) (*runtimev1pb.QueryVectorsResponseAlpha1, error) {
	if resp == nil {
		return &runtimev1pb.QueryVectorsResponseAlpha1{}, nil
	}

	out := &runtimev1pb.QueryVectorsResponseAlpha1{
		Matches: make([]*runtimev1pb.VectorMatch, 0, len(resp.Matches)),
		Metric:  runtimev1pb.DistanceMetric(resp.Metric),
	}
	for _, match := range resp.Matches {
		record, err := componentVectorRecordToProto(match.Record)
		if err != nil {
			return nil, err
		}
		out.Matches = append(out.Matches, &runtimev1pb.VectorMatch{
			Record: record,
			Score:  match.Score,
		})
	}
	return out, nil
}

func componentBatchQueryResultToProto(result *compvector.BatchQueryResult) *runtimev1pb.BatchQueryResultAlpha1 {
	if result.Error != nil {
		st := grpcstatus.Convert(componentError(result.Error))
		if st.Code() == codes.OK {
			// A batch error must never be OK.
			st = grpcstatus.New(codes.Unknown, "query failed without a reported cause")
		}
		return &runtimev1pb.BatchQueryResultAlpha1{
			Result: &runtimev1pb.BatchQueryResultAlpha1_Error{Error: st.Proto()},
		}
	}
	resp, err := componentQueryVectorsResponseToProto(result.Response)
	if err != nil {
		return &runtimev1pb.BatchQueryResultAlpha1{
			Result: &runtimev1pb.BatchQueryResultAlpha1_Error{Error: grpcstatus.Convert(err).Proto()},
		}
	}
	return &runtimev1pb.BatchQueryResultAlpha1{
		Result: &runtimev1pb.BatchQueryResultAlpha1_Response{Response: resp},
	}
}
