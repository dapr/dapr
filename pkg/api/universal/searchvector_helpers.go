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
	"time"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	compsearch "github.com/dapr/components-contrib/search"
	compvector "github.com/dapr/components-contrib/vector"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
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

// coded passes a component result through, wrapping a gRPC status error so
// that the store's retry policy can match its code. componentError undoes the
// wrapping.
func coded[T any](res T, err error) (T, error) {
	if err == nil {
		return res, nil
	}
	if st, ok := grpcstatus.FromError(err); ok {
		//nolint:gosec // canonical gRPC codes are 0-16
		return res, resiliency.NewCodeError(int32(st.Code()), err)
	}
	return res, err
}

// deletedOnRetry treats NOT_FOUND on a retried index or collection delete as
// success: an earlier attempt most likely deleted it before its response was
// lost, and either way it is gone. On the first attempt it is a genuine
// outcome, which retrying cannot change.
//
// Creates have no counterpart. ALREADY_EXISTS on a retry does not show that
// this request created and configured the resource: it may belong to another
// caller, or to an earlier attempt that is still running after a policy
// timeout and whose rollback will remove it.
func deletedOnRetry(ctx context.Context, err error) error {
	if grpcstatus.Code(err) != codes.NotFound {
		return err
	}
	if resiliency.GetAttempt(ctx) > 1 {
		return nil
	}
	return backoff.Permanent(err)
}

// waitTimeoutHeadroom is how much shorter than the store's resiliency timeout
// a wait must be. The provider reports an expired wait slightly after
// wait_timeout, and an attempt that ends within the runner's clock tolerance
// of its deadline is treated as a policy timeout and retried, which would
// resubmit the write.
func waitTimeoutHeadroom(timeout time.Duration) time.Duration {
	return max(timeout/10, 100*time.Millisecond)
}

// componentError normalizes a component error. Components are expected to
// return gRPC status errors carrying a canonical code. A resiliency timeout,
// cancellation or open circuit breaker surfaces from the policy runner as a
// plain error and is mapped to its canonical code; anything else is reported
// as INTERNAL so callers always see a canonical code.
func componentError(err error) error {
	if err == nil {
		return nil
	}
	// Without a retry policy the runner returns a permanent error as is, and
	// the status error may still carry its retry code.
	for unwrapping := true; unwrapping; {
		var permanent *backoff.PermanentError
		var codeErr resiliency.CodeError
		switch {
		case errors.As(err, &permanent):
			err = permanent.Unwrap()
		case errors.As(err, &codeErr):
			err = codeErr.Unwrap()
		default:
			unwrapping = false
		}
	}
	if _, ok := grpcstatus.FromError(err); ok {
		return err
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return grpcstatus.FromContextError(err).Err()
	case resiliency.IsCircuitBreakerError(err):
		return grpcstatus.Error(codes.Unavailable, err.Error())
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

// protoIndexingOptionsToComponent converts and validates the indexing options
// of a write. policy is the resiliency policy the write runs under; it may be
// nil.
func protoIndexingOptionsToComponent(ctx context.Context, opts *runtimev1pb.IndexingOptionsAlpha1, policy *resiliency.PolicyDefinition) (compsearch.IndexingOptions, error) {
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
	// The component runs under the store's resiliency policy, whose timeout
	// bounds every attempt. A wait that cannot finish within it is rejected
	// here: left to the component, the rejection would lose its building block
	// error code and be retried by the policy.
	if policy != nil && out.Mode == compsearch.IndexingModeWaitForCompletion {
		if timeout := policy.Timeout(); timeout > 0 {
			if headroom := waitTimeoutHeadroom(timeout); out.WaitTimeout > timeout-headroom {
				return out, newFieldError("options.wait_timeout",
					"wait_timeout (%s) must be at least %s shorter than the resiliency timeout (%s) configured for the store", out.WaitTimeout, headroom, timeout)
			}
		}
	}
	return out, nil
}

// waitExpiryNotRetried stops the resiliency policy retrying a write whose
// FAIL_REQUEST wait expired, which would resubmit it and wait again.
func waitExpiryNotRetried(opts compsearch.IndexingOptions, err error) error {
	if opts.Mode != compsearch.IndexingModeWaitForCompletion ||
		opts.OnWaitTimeout != compsearch.IndexingWaitTimeoutActionFailRequest {
		return err
	}
	if st, ok := grpcstatus.FromError(err); ok && st.Code() == codes.DeadlineExceeded {
		return backoff.Permanent(err)
	}
	return err
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
		// An unknown acknowledgement promises only that the write was accepted.
		return runtimev1pb.IndexAck_INDEX_ACK_QUEUED
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
