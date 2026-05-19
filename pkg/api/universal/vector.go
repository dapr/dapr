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
	"fmt"
	"io"

	compsearch "github.com/dapr/components-contrib/search"
	compvector "github.com/dapr/components-contrib/vector"
	"google.golang.org/protobuf/types/known/emptypb"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

func (a *Universal) CreateCollectionAlpha1(ctx context.Context, req *runtimev1pb.CreateCollectionRequestAlpha1) (*runtimev1pb.CreateCollectionResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetVector(storeName)
	if !ok {
		return nil, apierrors.VectorStore(storeName).NotFound()
	}

	compReq := &compvector.CreateCollectionRequest{
		Collection:     req.GetCollection(),
		Dimension:      req.GetDimension(),
		Metric:         compvector.DistanceMetric(int32(req.GetMetric())),
		MetadataSchema: protoIndexFieldsToComponent(req.GetMetadataSchema()),
		Metadata:       req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compvector.CreateCollectionResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	_, err := policyRunner(func(ctx context.Context) (*compvector.CreateCollectionResponse, error) {
		return comp.CreateCollection(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	return &runtimev1pb.CreateCollectionResponseAlpha1{}, nil
}

func (a *Universal) DropCollectionAlpha1(ctx context.Context, req *runtimev1pb.DropCollectionRequestAlpha1) (*emptypb.Empty, error) {
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetVector(storeName)
	if !ok {
		return nil, apierrors.VectorStore(storeName).NotFound()
	}

	compReq := &compvector.DropCollectionRequest{
		Collection: req.GetCollection(),
		Metadata:   req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[struct{}](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
		return struct{}{}, comp.DropCollection(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (a *Universal) DescribeCollectionAlpha1(ctx context.Context, req *runtimev1pb.DescribeCollectionRequestAlpha1) (*runtimev1pb.DescribeCollectionResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetVector(storeName)
	if !ok {
		return nil, apierrors.VectorStore(storeName).NotFound()
	}

	compReq := &compvector.DescribeCollectionRequest{
		Collection: req.GetCollection(),
		Metadata:   req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compvector.DescribeCollectionResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compvector.DescribeCollectionResponse, error) {
		return comp.DescribeCollection(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return &runtimev1pb.DescribeCollectionResponseAlpha1{}, nil
	}
	return &runtimev1pb.DescribeCollectionResponseAlpha1{
		Collection:     resp.Collection,
		Dimension:      resp.Dimension,
		Metric:         runtimev1pb.DistanceMetric(resp.Metric),
		MetadataSchema: componentIndexFieldsToProto(resp.MetadataSchema),
		VectorCount:    resp.VectorCount,
		Properties:     resp.Properties,
	}, nil
}

func (a *Universal) ListCollectionsAlpha1(ctx context.Context, req *runtimev1pb.ListCollectionsRequestAlpha1) (*runtimev1pb.ListCollectionsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetVector(storeName)
	if !ok {
		return nil, apierrors.VectorStore(storeName).NotFound()
	}

	compReq := &compvector.ListCollectionsRequest{Metadata: req.GetMetadata()}
	policyRunner := resiliency.NewRunner[*compvector.ListCollectionsResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compvector.ListCollectionsResponse, error) {
		return comp.ListCollections(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return &runtimev1pb.ListCollectionsResponseAlpha1{}, nil
	}
	return &runtimev1pb.ListCollectionsResponseAlpha1{Collections: resp.Collections}, nil
}

func (a *Universal) UpsertVectorsAlpha1(stream runtimev1pb.Dapr_UpsertVectorsAlpha1Server) error {
	ctx := stream.Context()
	var storeName, collection, idempotencyKey string
	var ack runtimev1pb.IndexAck
	var metadata map[string]string
	var vectors []compvector.Vec
	var seenFirst bool

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if !seenFirst {
			storeName = req.GetStoreName()
			collection = req.GetCollection()
			ack = req.GetAck()
			idempotencyKey = req.GetIdempotencyKey()
			metadata = req.GetMetadata()
			seenFirst = true
			if storeName == "" {
				return apierrors.VectorStore(storeName).MissingField("store_name")
			}
			if collection == "" {
				return apierrors.VectorStore(storeName).MissingField("collection")
			}
		} else if req.GetStoreName() != storeName || req.GetCollection() != collection || req.GetAck() != ack || req.GetIdempotencyKey() != idempotencyKey {
			return apierrors.VectorStore(storeName).InvalidRequest("stream", "upsert vector stream batches must share store_name, collection, ack, and idempotency_key")
		}

		converted := protoVectorsToComponent(req.GetVectors())
		vectors = append(vectors, converted...)
	}

	comp, ok := a.compStore.GetVector(storeName)
	if !ok {
		return apierrors.VectorStore(storeName).NotFound()
	}

	compReq := &compvector.UpsertRequest{
		Collection:     collection,
		Vectors:        vectors,
		Ack:            compsearch.IndexAck(int32(ack)),
		IdempotencyKey: idempotencyKey,
		Metadata:       metadata,
	}
	policyRunner := resiliency.NewRunner[*compvector.UpsertResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compvector.UpsertResponse, error) {
		return comp.Upsert(ctx, compReq)
	})
	if err != nil {
		return err
	}
	return stream.SendAndClose(componentUpsertResponseToProto(resp))
}

func (a *Universal) GetVectorsAlpha1(ctx context.Context, req *runtimev1pb.GetVectorsRequestAlpha1) (*runtimev1pb.GetVectorsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetVector(storeName)
	if !ok {
		return nil, apierrors.VectorStore(storeName).NotFound()
	}

	compReq := &compvector.GetRequest{
		Collection:      req.GetCollection(),
		IDs:             req.GetIds(),
		Namespace:       req.GetNamespace(),
		IncludeValues:   req.GetIncludeValues(),
		IncludeMetadata: req.GetIncludeMetadata(),
		Metadata:        req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compvector.GetResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compvector.GetResponse, error) {
		return comp.Get(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	return componentGetVectorsResponseToProto(resp)
}

func (a *Universal) DeleteVectorsAlpha1(ctx context.Context, req *runtimev1pb.DeleteVectorsRequestAlpha1) (*runtimev1pb.DeleteVectorsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetVector(storeName)
	if !ok {
		return nil, apierrors.VectorStore(storeName).NotFound()
	}

	selector := compvector.DeleteSelector{}
	if ids := req.GetIds(); ids != nil {
		selector.IDs = ids.GetIds()
	} else if filter := req.GetFilter(); filter != nil {
		selector.Filter = protoStructToMap(filter.GetFilter())
	}
	compReq := &compvector.DeleteRequest{
		Collection: req.GetCollection(),
		Namespace:  req.GetNamespace(),
		Selector:   selector,
		Ack:        compsearch.IndexAck(int32(req.GetAck())),
		Metadata:   req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compvector.DeleteResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compvector.DeleteResponse, error) {
		return comp.Delete(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	return componentDeleteVectorsResponseToProto(resp), nil
}

func (a *Universal) QueryVectorsAlpha1(req *runtimev1pb.QueryVectorsRequestAlpha1, stream runtimev1pb.Dapr_QueryVectorsAlpha1Server) error {
	ctx := stream.Context()
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetVector(storeName)
	if !ok {
		return apierrors.VectorStore(storeName).NotFound()
	}

	compReq, err := protoQueryVectorsRequestToComponent(req)
	if err != nil {
		return apierrors.VectorStore(storeName).InvalidRequest("query", err.Error())
	}
	policyRunner := resiliency.NewRunner[*compvector.QueryResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Vector),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compvector.QueryResponse, error) {
		return comp.Query(ctx, compReq)
	})
	if err != nil {
		return err
	}
	protoResp, err := componentQueryVectorsResponseToProto(resp)
	if err != nil {
		return err
	}
	return stream.Send(protoResp)
}

func (a *Universal) BatchQueryVectorsAlpha1(ctx context.Context, req *runtimev1pb.BatchQueryVectorsRequestAlpha1) (*runtimev1pb.BatchQueryVectorsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetVector(storeName)
	if !ok {
		return nil, apierrors.VectorStore(storeName).NotFound()
	}

	queries := make([]compvector.QueryRequest, 0, len(req.GetQueries()))
	for i, query := range req.GetQueries() {
		compQuery, err := protoQueryVectorsRequestToComponent(query)
		if err != nil {
			return nil, apierrors.VectorStore(storeName).InvalidRequest("queries", fmt.Sprintf("query %d: %s", i, err))
		}
		queries = append(queries, *compQuery)
	}
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
		return nil, err
	}
	return componentBatchQueryVectorsResponseToProto(resp)
}

func protoVectorsToComponent(vectors []*runtimev1pb.Vector) []compvector.Vec {
	if len(vectors) == 0 {
		return nil
	}

	out := make([]compvector.Vec, 0, len(vectors))
	for _, v := range vectors {
		out = append(out, compvector.Vec{
			ID:        v.GetId(),
			Values:    v.GetValues(),
			Metadata:  protoStructToMap(v.GetMetadata()),
			Namespace: v.GetNamespace(),
		})
	}
	return out
}

func componentVectorsToProto(vectors []compvector.Vec) ([]*runtimev1pb.Vector, error) {
	if len(vectors) == 0 {
		return nil, nil
	}

	out := make([]*runtimev1pb.Vector, 0, len(vectors))
	for _, v := range vectors {
		metadata, err := mapToProtoStruct(v.Metadata)
		if err != nil {
			return nil, err
		}
		out = append(out, &runtimev1pb.Vector{
			Id:        v.ID,
			Values:    v.Values,
			Metadata:  metadata,
			Namespace: v.Namespace,
		})
	}
	return out, nil
}

func componentUpsertResponseToProto(resp *compvector.UpsertResponse) *runtimev1pb.UpsertVectorsResponseAlpha1 {
	if resp == nil {
		return &runtimev1pb.UpsertVectorsResponseAlpha1{}
	}
	return &runtimev1pb.UpsertVectorsResponseAlpha1{
		Results: componentOperationResultsToProto(resp.Results),
		Ack:     runtimev1pb.IndexAck(resp.Ack),
	}
}

func componentGetVectorsResponseToProto(resp *compvector.GetResponse) (*runtimev1pb.GetVectorsResponseAlpha1, error) {
	if resp == nil {
		return &runtimev1pb.GetVectorsResponseAlpha1{}, nil
	}
	vectors, err := componentVectorsToProto(resp.Vectors)
	if err != nil {
		return nil, err
	}
	return &runtimev1pb.GetVectorsResponseAlpha1{Vectors: vectors}, nil
}

func componentDeleteVectorsResponseToProto(resp *compvector.DeleteResponse) *runtimev1pb.DeleteVectorsResponseAlpha1 {
	if resp == nil {
		return &runtimev1pb.DeleteVectorsResponseAlpha1{}
	}
	return &runtimev1pb.DeleteVectorsResponseAlpha1{
		Results:      componentOperationResultsToProto(resp.Results),
		DeletedCount: resp.DeletedCount,
		Ack:          runtimev1pb.IndexAck(resp.Ack),
	}
}

func protoQueryVectorsRequestToComponent(req *runtimev1pb.QueryVectorsRequestAlpha1) (*compvector.QueryRequest, error) {
	compReq := &compvector.QueryRequest{
		Collection:        req.GetCollection(),
		Namespace:         req.GetNamespace(),
		Filter:            protoStructToMap(req.GetFilter()),
		TopK:              req.GetTopK(),
		IncludeValues:     req.GetIncludeValues(),
		IncludeMetadata:   req.GetIncludeMetadata(),
		ContinuationToken: req.GetContinuationToken(),
		Metadata:          req.GetMetadata(),
	}
	if req.ScoreThreshold != nil {
		threshold := req.GetScoreThreshold()
		compReq.ScoreThreshold = &threshold
	}
	hasVector := req.GetVector() != nil
	hasByID := req.GetById() != ""
	if hasVector == hasByID {
		return nil, fmt.Errorf("exactly one of vector or by_id must be set")
	}
	if queryVector := req.GetVector(); queryVector != nil {
		compReq.QueryVector = queryVector.GetValues()
	} else {
		compReq.QueryByID = req.GetById()
	}
	return compReq, nil
}

func componentQueryVectorsResponseToProto(resp *compvector.QueryResponse) (*runtimev1pb.QueryVectorsResponseAlpha1, error) {
	if resp == nil {
		return &runtimev1pb.QueryVectorsResponseAlpha1{}, nil
	}
	hits, err := componentVectorHitsToProto(resp.Hits)
	if err != nil {
		return nil, err
	}
	return &runtimev1pb.QueryVectorsResponseAlpha1{
		Hits:              hits,
		ContinuationToken: resp.ContinuationToken,
	}, nil
}

func componentBatchQueryVectorsResponseToProto(resp *compvector.BatchQueryResponse) (*runtimev1pb.BatchQueryVectorsResponseAlpha1, error) {
	if resp == nil {
		return &runtimev1pb.BatchQueryVectorsResponseAlpha1{}, nil
	}

	results := make([]*runtimev1pb.BatchQueryResult, 0, len(resp.Results))
	for _, result := range resp.Results {
		hits, err := componentVectorHitsToProto(result.Hits)
		if err != nil {
			return nil, err
		}
		results = append(results, &runtimev1pb.BatchQueryResult{
			QueryIndex:   result.QueryIndex,
			Hits:         hits,
			ErrorCode:    result.ErrorCode,
			ErrorMessage: result.ErrorMessage,
		})
	}
	return &runtimev1pb.BatchQueryVectorsResponseAlpha1{Results: results}, nil
}

func componentVectorHitsToProto(hits []compvector.Hit) ([]*runtimev1pb.VectorHit, error) {
	if len(hits) == 0 {
		return nil, nil
	}

	out := make([]*runtimev1pb.VectorHit, 0, len(hits))
	for _, hit := range hits {
		vectors, err := componentVectorsToProto([]compvector.Vec{hit.Vector})
		if err != nil {
			return nil, err
		}
		var v *runtimev1pb.Vector
		if len(vectors) > 0 {
			v = vectors[0]
		}
		out = append(out, &runtimev1pb.VectorHit{
			Vector:   v,
			Distance: hit.Distance,
		})
	}
	return out, nil
}
