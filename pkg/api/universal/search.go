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
	"io"

	compsearch "github.com/dapr/components-contrib/search"
	"google.golang.org/protobuf/types/known/emptypb"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

func (a *Universal) CreateIndexAlpha1(ctx context.Context, req *runtimev1pb.CreateIndexRequestAlpha1) (*runtimev1pb.CreateIndexResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetSearch(storeName)
	if !ok {
		return nil, apierrors.SearchStore(storeName).NotFound()
	}

	compReq := &compsearch.CreateIndexRequest{
		Index:    req.GetIndex(),
		Fields:   protoIndexFieldsToComponent(req.GetFields()),
		Metadata: req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compsearch.CreateIndexResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	_, err := policyRunner(func(ctx context.Context) (*compsearch.CreateIndexResponse, error) {
		return comp.CreateIndex(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	return &runtimev1pb.CreateIndexResponseAlpha1{}, nil
}

func (a *Universal) DropIndexAlpha1(ctx context.Context, req *runtimev1pb.DropIndexRequestAlpha1) (*emptypb.Empty, error) {
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetSearch(storeName)
	if !ok {
		return nil, apierrors.SearchStore(storeName).NotFound()
	}

	compReq := &compsearch.DropIndexRequest{
		Index:    req.GetIndex(),
		Metadata: req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[struct{}](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	_, err := policyRunner(func(ctx context.Context) (struct{}, error) {
		return struct{}{}, comp.DropIndex(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (a *Universal) DescribeIndexAlpha1(ctx context.Context, req *runtimev1pb.DescribeIndexRequestAlpha1) (*runtimev1pb.DescribeIndexResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetSearch(storeName)
	if !ok {
		return nil, apierrors.SearchStore(storeName).NotFound()
	}

	compReq := &compsearch.DescribeIndexRequest{
		Index:    req.GetIndex(),
		Metadata: req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compsearch.DescribeIndexResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compsearch.DescribeIndexResponse, error) {
		return comp.DescribeIndex(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return &runtimev1pb.DescribeIndexResponseAlpha1{}, nil
	}
	return &runtimev1pb.DescribeIndexResponseAlpha1{
		Index:         resp.Index,
		Fields:        componentIndexFieldsToProto(resp.Fields),
		DocumentCount: resp.DocumentCount,
		Properties:    resp.Properties,
	}, nil
}

func (a *Universal) ListIndexesAlpha1(ctx context.Context, req *runtimev1pb.ListIndexesRequestAlpha1) (*runtimev1pb.ListIndexesResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetSearch(storeName)
	if !ok {
		return nil, apierrors.SearchStore(storeName).NotFound()
	}

	compReq := &compsearch.ListIndexesRequest{Metadata: req.GetMetadata()}
	policyRunner := resiliency.NewRunner[*compsearch.ListIndexesResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compsearch.ListIndexesResponse, error) {
		return comp.ListIndexes(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return &runtimev1pb.ListIndexesResponseAlpha1{}, nil
	}
	return &runtimev1pb.ListIndexesResponseAlpha1{Indexes: resp.Indexes}, nil
}

func (a *Universal) IndexDocumentsAlpha1(stream runtimev1pb.Dapr_IndexDocumentsAlpha1Server) error {
	ctx := stream.Context()
	var storeName, index, idempotencyKey string
	var ack runtimev1pb.IndexAck
	var metadata map[string]string
	var docs []compsearch.Document
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
			index = req.GetIndex()
			ack = req.GetAck()
			idempotencyKey = req.GetIdempotencyKey()
			metadata = req.GetMetadata()
			seenFirst = true
			if storeName == "" {
				return apierrors.SearchStore(storeName).MissingField("store_name")
			}
			if index == "" {
				return apierrors.SearchStore(storeName).MissingField("index")
			}
		} else if req.GetStoreName() != storeName || req.GetIndex() != index || req.GetAck() != ack || req.GetIdempotencyKey() != idempotencyKey {
			return apierrors.SearchStore(storeName).InvalidRequest("stream", "index document stream batches must share store_name, index, ack, and idempotency_key")
		}

		converted, err := protoSearchDocumentsToComponent(req.GetDocuments())
		if err != nil {
			return err
		}
		docs = append(docs, converted...)
	}

	comp, ok := a.compStore.GetSearch(storeName)
	if !ok {
		return apierrors.SearchStore(storeName).NotFound()
	}

	compReq := &compsearch.IndexDocumentsRequest{
		Index:          index,
		Documents:      docs,
		Ack:            compsearch.IndexAck(int32(ack)),
		IdempotencyKey: idempotencyKey,
		Metadata:       metadata,
	}
	policyRunner := resiliency.NewRunner[*compsearch.IndexDocumentsResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compsearch.IndexDocumentsResponse, error) {
		return comp.IndexDocuments(ctx, compReq)
	})
	if err != nil {
		return err
	}
	return stream.SendAndClose(componentIndexDocumentsResponseToProto(resp))
}

func (a *Universal) DeleteDocumentsAlpha1(ctx context.Context, req *runtimev1pb.DeleteDocumentsRequestAlpha1) (*runtimev1pb.DeleteDocumentsResponseAlpha1, error) {
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetSearch(storeName)
	if !ok {
		return nil, apierrors.SearchStore(storeName).NotFound()
	}

	compReq := &compsearch.DeleteDocumentsRequest{
		Index:    req.GetIndex(),
		IDs:      req.GetIds(),
		Ack:      compsearch.IndexAck(int32(req.GetAck())),
		Metadata: req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compsearch.DeleteDocumentsResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compsearch.DeleteDocumentsResponse, error) {
		return comp.DeleteDocuments(ctx, compReq)
	})
	if err != nil {
		return nil, err
	}
	return componentDeleteDocumentsResponseToProto(resp), nil
}

func (a *Universal) SearchAlpha1(req *runtimev1pb.SearchRequestAlpha1, stream runtimev1pb.Dapr_SearchAlpha1Server) error {
	ctx := stream.Context()
	storeName := req.GetStoreName()
	comp, ok := a.compStore.GetSearch(storeName)
	if !ok {
		return apierrors.SearchStore(storeName).NotFound()
	}

	compReq := &compsearch.SearchRequest{
		Index:             req.GetIndex(),
		Text:              req.GetText(),
		Native:            protoStructToMap(req.GetNative()),
		Filter:            protoStructToMap(req.GetFilter()),
		TopK:              req.GetTopK(),
		ReturnFields:      req.GetReturnFields(),
		IncludeContent:    req.GetIncludeContent(),
		SearchFields:      req.GetSearchFields(),
		Sort:              protoSortClausesToComponent(req.GetSort()),
		HighlightFields:   req.GetHighlightFields(),
		ContinuationToken: req.GetContinuationToken(),
		Metadata:          req.GetMetadata(),
	}
	policyRunner := resiliency.NewRunner[*compsearch.SearchResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(storeName, resiliency.Search),
	)
	resp, err := policyRunner(func(ctx context.Context) (*compsearch.SearchResponse, error) {
		return comp.Search(ctx, compReq)
	})
	if err != nil {
		return err
	}

	// Alpha1 server streaming sends one page per component invocation; callers can re-issue with continuation_token for more pages.
	protoResp, err := componentSearchResponseToProto(resp)
	if err != nil {
		return err
	}
	return stream.Send(protoResp)
}

func protoSearchDocumentsToComponent(docs []*runtimev1pb.SearchDocument) ([]compsearch.Document, error) {
	if len(docs) == 0 {
		return nil, nil
	}

	out := make([]compsearch.Document, 0, len(docs))
	for _, doc := range docs {
		out = append(out, compsearch.Document{
			ID:       doc.GetId(),
			Content:  protoStructToMap(doc.GetContent()),
			Metadata: doc.GetMetadata(),
		})
	}
	return out, nil
}

func componentSearchDocumentsToProto(docs []compsearch.Document) ([]*runtimev1pb.SearchDocument, error) {
	if len(docs) == 0 {
		return nil, nil
	}

	out := make([]*runtimev1pb.SearchDocument, 0, len(docs))
	for _, doc := range docs {
		content, err := mapToProtoStruct(doc.Content)
		if err != nil {
			return nil, err
		}
		out = append(out, &runtimev1pb.SearchDocument{
			Id:       doc.ID,
			Content:  content,
			Metadata: doc.Metadata,
		})
	}
	return out, nil
}

func componentIndexDocumentsResponseToProto(resp *compsearch.IndexDocumentsResponse) *runtimev1pb.IndexDocumentsResponseAlpha1 {
	if resp == nil {
		return &runtimev1pb.IndexDocumentsResponseAlpha1{}
	}
	return &runtimev1pb.IndexDocumentsResponseAlpha1{
		Results: componentOperationResultsToProto(resp.Results),
		Ack:     runtimev1pb.IndexAck(resp.Ack),
	}
}

func componentDeleteDocumentsResponseToProto(resp *compsearch.DeleteDocumentsResponse) *runtimev1pb.DeleteDocumentsResponseAlpha1 {
	if resp == nil {
		return &runtimev1pb.DeleteDocumentsResponseAlpha1{}
	}
	return &runtimev1pb.DeleteDocumentsResponseAlpha1{
		Results: componentOperationResultsToProto(resp.Results),
		Ack:     runtimev1pb.IndexAck(resp.Ack),
	}
}

func protoSortClausesToComponent(clauses []*runtimev1pb.SortClause) []compsearch.SortClause {
	if len(clauses) == 0 {
		return nil
	}

	out := make([]compsearch.SortClause, 0, len(clauses))
	for _, clause := range clauses {
		out = append(out, compsearch.SortClause{
			Field: clause.GetField(),
			Order: compsearch.SortOrder(int32(clause.GetOrder())),
		})
	}
	return out
}

func componentSearchResponseToProto(resp *compsearch.SearchResponse) (*runtimev1pb.SearchResponseAlpha1, error) {
	if resp == nil {
		return &runtimev1pb.SearchResponseAlpha1{}, nil
	}

	hits, err := componentSearchHitsToProto(resp.Hits)
	if err != nil {
		return nil, err
	}
	return &runtimev1pb.SearchResponseAlpha1{
		Hits:              hits,
		TotalHits:         resp.TotalHits,
		ContinuationToken: resp.ContinuationToken,
	}, nil
}

func componentSearchHitsToProto(hits []compsearch.Hit) ([]*runtimev1pb.SearchHit, error) {
	if len(hits) == 0 {
		return nil, nil
	}

	out := make([]*runtimev1pb.SearchHit, 0, len(hits))
	for _, hit := range hits {
		docs, err := componentSearchDocumentsToProto([]compsearch.Document{hit.Document})
		if err != nil {
			return nil, err
		}
		var doc *runtimev1pb.SearchDocument
		if len(docs) > 0 {
			doc = docs[0]
		}
		out = append(out, &runtimev1pb.SearchHit{
			Document:   doc,
			Score:      hit.Score,
			Highlights: componentHighlightsToProto(hit.Highlights),
		})
	}
	return out, nil
}

func componentHighlightsToProto(highlights map[string]compsearch.FieldHighlight) map[string]*runtimev1pb.FieldHighlight {
	if len(highlights) == 0 {
		return nil
	}

	out := make(map[string]*runtimev1pb.FieldHighlight, len(highlights))
	for field, highlight := range highlights {
		snippets := make([]*runtimev1pb.Snippet, 0, len(highlight.Snippets))
		for _, snippet := range highlight.Snippets {
			snippets = append(snippets, &runtimev1pb.Snippet{Text: snippet.Text})
		}
		out[field] = &runtimev1pb.FieldHighlight{Snippets: snippets}
	}
	return out
}
