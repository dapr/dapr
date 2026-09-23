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
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

func (a *Universal) CreateIndexAlpha1(ctx context.Context, req *runtimev1pb.CreateIndexRequestAlpha1) (*emptypb.Empty, error) {
	storeName := req.GetStoreName()
	comp, err := a.getSearchStore(storeName)
	if err != nil {
		return nil, err
	}

	compReq := &compsearch.CreateIndexRequest{
		Index:    req.GetIndex(),
		Metadata: req.GetMetadata(),
	}
	err = runSearchPolicy(ctx, a, storeName, func(ctx context.Context) error {
		return comp.CreateIndex(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (a *Universal) GetIndexAlpha1(ctx context.Context, req *runtimev1pb.GetIndexRequestAlpha1) (*runtimev1pb.GetIndexResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getSearchStore(storeName)
	if err != nil {
		return nil, err
	}

	compReq := &compsearch.GetIndexRequest{
		Index:    req.GetIndex(),
		Metadata: req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compsearch.GetIndexResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compsearch.GetIndexResponse, error) {
		return comp.GetIndex(ctx, compReq)
	})
	if err != nil {
		return nil, componentError(err)
	}
	if resp == nil {
		return &runtimev1pb.GetIndexResponseAlpha1{}, nil
	}
	return &runtimev1pb.GetIndexResponseAlpha1{
		Index:         resp.Index,
		DocumentCount: resp.DocumentCount,
		Properties:    resp.Properties,
	}, nil
}

func (a *Universal) ListIndexesAlpha1(ctx context.Context, req *runtimev1pb.ListIndexesRequestAlpha1) (*runtimev1pb.ListIndexesResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getSearchStore(storeName)
	if err != nil {
		return nil, err
	}

	compReq := &compsearch.ListIndexesRequest{Metadata: req.GetMetadata()}
	policyRunner := resiliency.NewRunner[*compsearch.ListIndexesResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compsearch.ListIndexesResponse, error) {
		return comp.ListIndexes(ctx, compReq)
	})
	if err != nil {
		return nil, componentError(err)
	}
	if resp == nil {
		return &runtimev1pb.ListIndexesResponseAlpha1{}, nil
	}
	return &runtimev1pb.ListIndexesResponseAlpha1{Indexes: resp.Indexes}, nil
}

func (a *Universal) DeleteIndexAlpha1(ctx context.Context, req *runtimev1pb.DeleteIndexRequestAlpha1) (*emptypb.Empty, error) {
	storeName := req.GetStoreName()
	comp, err := a.getSearchStore(storeName)
	if err != nil {
		return nil, err
	}

	compReq := &compsearch.DeleteIndexRequest{
		Index:    req.GetIndex(),
		Metadata: req.GetMetadata(),
	}
	err = runSearchPolicy(ctx, a, storeName, func(ctx context.Context) error {
		return comp.DeleteIndex(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// IndexDocumentsAlpha1 is a keyed upsert: every document carries a non-empty,
// caller-supplied ID that is unique within the request, which makes retrying
// the same request idempotent. Documents whose content is not a JSON object
// are reported in failed_items and never reach the provider.
func (a *Universal) IndexDocumentsAlpha1(ctx context.Context, req *runtimev1pb.IndexDocumentsRequestAlpha1) (*runtimev1pb.IndexDocumentsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getSearchStore(storeName)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(req.GetDocuments()))
	for _, doc := range req.GetDocuments() {
		ids = append(ids, doc.GetId())
	}
	if err := compsearch.ValidateWriteIDs(ids); err != nil {
		return nil, searchInvalidRequest(storeName, fieldErrorFrom("documents.id", err))
	}

	options, err := protoIndexingOptionsToComponent(ctx, req.GetOptions())
	if err != nil {
		return nil, searchInvalidRequest(storeName, err)
	}

	docs := make([]compsearch.Document, 0, len(req.GetDocuments()))
	var rejected []compsearch.FailedItem
	for _, doc := range req.GetDocuments() {
		if err := compsearch.ValidateDocumentContent(doc.GetContent()); err != nil {
			rejected = append(rejected, compsearch.FailedItem{ID: doc.GetId(), Error: grpcstatus.Convert(err)})
			continue
		}
		docs = append(docs, compsearch.Document{
			ID:       doc.GetId(),
			Content:  doc.GetContent(),
			Metadata: doc.GetMetadata(),
		})
	}
	if len(docs) == 0 {
		// Nothing valid to write; the (empty) write is complete.
		return &runtimev1pb.IndexDocumentsResponseAlpha1{
			FailedItems: componentFailedItemsToProto(rejected),
			Ack:         runtimev1pb.IndexAck_INDEX_ACK_COMPLETED,
		}, nil
	}

	compReq := &compsearch.IndexDocumentsRequest{
		Index:     req.GetIndex(),
		Documents: docs,
		Metadata:  req.GetMetadata(),
		Options:   options,
	}
	policyRunner := resiliency.NewRunner[*compsearch.IndexDocumentsResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compsearch.IndexDocumentsResponse, error) {
		return comp.IndexDocuments(ctx, compReq)
	})
	if err != nil {
		return nil, componentError(err)
	}
	if resp == nil {
		return &runtimev1pb.IndexDocumentsResponseAlpha1{
			FailedItems: componentFailedItemsToProto(rejected),
			Ack:         runtimev1pb.IndexAck_INDEX_ACK_COMPLETED,
		}, nil
	}
	return &runtimev1pb.IndexDocumentsResponseAlpha1{
		FailedItems: componentFailedItemsToProto(append(rejected, resp.FailedItems...)),
		Ack:         componentIndexAckToProto(resp.Ack),
	}, nil
}

func (a *Universal) GetDocumentsAlpha1(ctx context.Context, req *runtimev1pb.GetDocumentsRequestAlpha1) (*runtimev1pb.GetDocumentsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getSearchStore(storeName)
	if err != nil {
		return nil, err
	}

	compReq := &compsearch.GetDocumentsRequest{
		Index:          req.GetIndex(),
		IDs:            req.GetIds(),
		IncludeContent: req.GetIncludeContent(),
		Metadata:       req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compsearch.GetDocumentsResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compsearch.GetDocumentsResponse, error) {
		return comp.GetDocuments(ctx, compReq)
	})
	if err != nil {
		return nil, componentError(err)
	}
	if resp == nil {
		return &runtimev1pb.GetDocumentsResponseAlpha1{}, nil
	}
	return &runtimev1pb.GetDocumentsResponseAlpha1{
		Documents: componentDocumentsToProto(resp.Documents),
	}, nil
}

// DeleteDocumentsAlpha1 is a write and shares the acknowledgement model of
// IndexDocumentsAlpha1. IDs that do not exist are not an error.
func (a *Universal) DeleteDocumentsAlpha1(ctx context.Context, req *runtimev1pb.DeleteDocumentsRequestAlpha1) (*runtimev1pb.DeleteDocumentsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getSearchStore(storeName)
	if err != nil {
		return nil, err
	}

	options, err := protoIndexingOptionsToComponent(ctx, req.GetOptions())
	if err != nil {
		return nil, searchInvalidRequest(storeName, err)
	}

	compReq := &compsearch.DeleteDocumentsRequest{
		Index:    req.GetIndex(),
		IDs:      req.GetIds(),
		Metadata: req.GetMetadata(),
		Options:  options,
	}
	policyRunner := resiliency.NewRunner[*compsearch.DeleteDocumentsResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compsearch.DeleteDocumentsResponse, error) {
		return comp.DeleteDocuments(ctx, compReq)
	})
	if err != nil {
		return nil, componentError(err)
	}
	if resp == nil {
		return &runtimev1pb.DeleteDocumentsResponseAlpha1{Ack: runtimev1pb.IndexAck_INDEX_ACK_COMPLETED}, nil
	}
	return &runtimev1pb.DeleteDocumentsResponseAlpha1{Ack: componentIndexAckToProto(resp.Ack)}, nil
}

func (a *Universal) SearchAlpha1(ctx context.Context, req *runtimev1pb.SearchRequestAlpha1) (*runtimev1pb.SearchResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, err := a.getSearchStore(storeName)
	if err != nil {
		return nil, err
	}

	compReq := &compsearch.SearchRequest{
		Index:             req.GetIndex(),
		Text:              req.GetText(),
		Native:            protoStructToMap(req.GetNative()),
		Filter:            protoStructToMap(req.GetFilter()),
		TopK:              req.GetTopK(),
		ContinuationToken: req.GetContinuationToken(),
		ReturnFields:      req.GetReturnFields(),
		IncludeContent:    req.GetIncludeContent(),
		SearchFields:      req.GetSearchFields(),
		Sort:              protoSortClausesToComponent(req.GetSort()),
		HighlightFields:   req.GetHighlightFields(),
		Metadata:          req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compsearch.SearchResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compsearch.SearchResponse, error) {
		return comp.Search(ctx, compReq)
	})
	if err != nil {
		return nil, componentError(err)
	}
	if resp == nil {
		return &runtimev1pb.SearchResponseAlpha1{}, nil
	}

	out := &runtimev1pb.SearchResponseAlpha1{
		Hits:              make([]*runtimev1pb.SearchHit, 0, len(resp.Hits)),
		TotalHits:         resp.TotalHits,
		ContinuationToken: resp.ContinuationToken,
		TotalHitsRelation: runtimev1pb.TotalHitsRelation(resp.TotalHitsRelation),
	}
	if resp.TotalHits == nil {
		// The relation only describes a total that was supplied.
		out.TotalHitsRelation = runtimev1pb.TotalHitsRelation_TOTAL_HITS_RELATION_UNSPECIFIED
	}
	for _, hit := range resp.Hits {
		out.Hits = append(out.Hits, &runtimev1pb.SearchHit{
			Document:   componentDocumentToProto(hit.Document),
			Score:      hit.Score,
			Highlights: hit.Highlights,
		})
	}
	return out, nil
}

// runSearchPolicy runs fn under the search component's resiliency policy and
// normalizes the component error.
func runSearchPolicy(ctx context.Context, a *Universal, storeName string, fn func(ctx context.Context) error) error {
	policyRunner := resiliency.NewRunner[struct{}](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return componentError(err)
}
