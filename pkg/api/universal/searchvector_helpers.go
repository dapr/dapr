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
	"errors"
	"fmt"

	compsearch "github.com/dapr/components-contrib/search"
	compvector "github.com/dapr/components-contrib/vector"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func protoStructToMap(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}
	return s.AsMap()
}

// fieldError is a request validation failure the runtime detects before the
// provider is invoked, carrying the offending request field so it can be
// reported as a field violation.
type fieldError struct {
	field string
	msg   string
}

func (e *fieldError) Error() string {
	return e.msg
}

func newFieldError(field, format string, args ...any) error {
	return &fieldError{field: field, msg: fmt.Sprintf(format, args...)}
}

// fieldErrorFrom re-attributes a validation error raised by the shared
// component-side validators to a request field.
func fieldErrorFrom(field string, err error) error {
	return &fieldError{field: field, msg: grpcstatus.Convert(err).Message()}
}

// getSearchStore resolves a search component by name. A store name that does
// not resolve while no search store is registered at all is reported as
// "not configured" rather than "not found", matching the other building
// blocks.
func (a *Universal) getSearchStore(storeName string) (compsearch.Search, error) {
	comp, ok := a.compStore.GetSearch(storeName)
	if ok {
		return comp, nil
	}
	if a.compStore.SearchesLen() == 0 {
		return nil, apierrors.SearchStore(storeName).NotConfigured()
	}
	return nil, apierrors.SearchStore(storeName).NotFound()
}

// getVectorStore is the vector counterpart of getSearchStore.
func (a *Universal) getVectorStore(storeName string) (compvector.Vector, error) {
	comp, ok := a.compStore.GetVector(storeName)
	if ok {
		return comp, nil
	}
	if a.compStore.VectorsLen() == 0 {
		return nil, apierrors.VectorStore(storeName).NotConfigured()
	}
	return nil, apierrors.VectorStore(storeName).NotFound()
}

// searchInvalidRequest reports a runtime-detected validation failure as an
// INVALID_ARGUMENT search error. Errors raised by the component itself are
// never routed through here: their canonical code and any google.rpc.ErrorInfo
// details must reach the caller unchanged.
func searchInvalidRequest(storeName string, err error) error {
	var fe *fieldError
	if errors.As(err, &fe) {
		return apierrors.SearchStore(storeName).InvalidRequest(fe.field, fe.msg)
	}
	return componentError(err)
}

// vectorInvalidRequest is the vector counterpart of searchInvalidRequest.
func vectorInvalidRequest(storeName string, err error) error {
	var fe *fieldError
	if errors.As(err, &fe) {
		return apierrors.VectorStore(storeName).InvalidRequest(fe.field, fe.msg)
	}
	return componentError(err)
}

// componentError normalizes a component error. Components are expected to
// return gRPC status errors carrying a canonical code; anything else is
// reported as INTERNAL so callers always see a canonical code.
func componentError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := grpcstatus.FromError(err); ok {
		return err
	}
	return grpcstatus.Error(codes.Internal, err.Error())
}

func protoSortClausesToComponent(clauses []*runtimev1pb.SortClause) []compsearch.SortClause {
	if len(clauses) == 0 {
		return nil
	}

	out := make([]compsearch.SortClause, 0, len(clauses))
	for _, clause := range clauses {
		out = append(out, compsearch.SortClause{
			Field: clause.GetField(),
			Order: compsearch.SortOrder(clause.GetOrder()),
		})
	}
	return out
}

func protoIndexingOptionsToComponent(ctx context.Context, opts *runtimev1pb.IndexingOptionsAlpha1) (compsearch.IndexingOptions, error) {
	out := compsearch.IndexingOptions{
		Mode:          compsearch.IndexingMode(opts.GetMode()),
		OnWaitTimeout: compsearch.IndexingWaitTimeoutAction(opts.GetOnWaitTimeout()),
	}
	if wait := opts.GetWaitTimeout(); wait != nil {
		if err := wait.CheckValid(); err != nil {
			return out, newFieldError("options.wait_timeout", "invalid wait_timeout: %s", err)
		}
		out.WaitTimeout = wait.AsDuration()
	}
	// Validate the portable rules before the provider is invoked. Whether the
	// provider offers a native queued acknowledgement is only known to the
	// component, so CONTINUE_ASYNC is accepted here and rejected there.
	if err := compsearch.ValidateIndexingOptions(ctx, out, true); err != nil {
		return out, fieldErrorFrom("options", err)
	}
	return out, nil
}

func componentIndexAckToProto(ack compsearch.IndexAck) runtimev1pb.IndexAck {
	switch ack {
	case compsearch.IndexAckQueued:
		return runtimev1pb.IndexAck_INDEX_ACK_QUEUED
	case compsearch.IndexAckCompleted:
		return runtimev1pb.IndexAck_INDEX_ACK_COMPLETED
	case compsearch.IndexAckUnspecified:
		// A successful write always reaches an acknowledgement boundary; a
		// component that leaves this unset completed the write.
		return runtimev1pb.IndexAck_INDEX_ACK_COMPLETED
	default:
		return runtimev1pb.IndexAck_INDEX_ACK_COMPLETED
	}
}

func componentFailedItemsToProto(items []compsearch.FailedItem) []*runtimev1pb.FailedItem {
	if len(items) == 0 {
		return nil
	}

	out := make([]*runtimev1pb.FailedItem, 0, len(items))
	for _, item := range items {
		st := item.Error
		if st == nil || st.Code() == codes.OK {
			// FailedItem.error must never be OK.
			st = grpcstatus.New(codes.Unknown, "item failed without a reported cause")
		}
		out = append(out, &runtimev1pb.FailedItem{
			Id:    item.ID,
			Error: st.Proto(),
		})
	}
	return out
}

func componentDocumentToProto(doc compsearch.Document) *runtimev1pb.SearchDocument {
	return &runtimev1pb.SearchDocument{
		Id:       doc.ID,
		Content:  doc.Content,
		Metadata: doc.Metadata,
	}
}

func componentDocumentsToProto(docs []compsearch.Document) []*runtimev1pb.SearchDocument {
	if len(docs) == 0 {
		return nil
	}

	out := make([]*runtimev1pb.SearchDocument, 0, len(docs))
	for _, doc := range docs {
		out = append(out, componentDocumentToProto(doc))
	}
	return out
}
