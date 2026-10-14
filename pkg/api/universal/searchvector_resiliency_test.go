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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	compsearch "github.com/dapr/components-contrib/search"
	compvector "github.com/dapr/components-contrib/vector"

	"github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/messages/errorcodes"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

const writePolicyTimeout = 2 * time.Second

// writeResiliency targets both test stores with a timeout and a retry policy.
var writeResiliency = &v1alpha1.Resiliency{
	Spec: v1alpha1.ResiliencySpec{
		Policies: v1alpha1.Policies{
			Timeouts: map[string]string{"write": writePolicyTimeout.String()},
			Retries: map[string]v1alpha1.Retry{
				"twice": {MaxRetries: new(2), Policy: "constant", Duration: "10ms"},
			},
		},
		Targets: v1alpha1.Targets{
			Components: map[string]v1alpha1.ComponentPolicyNames{
				testSearchStore: {Outbound: v1alpha1.PolicyNames{Timeout: "write", Retry: "twice"}},
				testVectorStore: {Outbound: v1alpha1.PolicyNames{Timeout: "write", Retry: "twice"}},
			},
		},
	},
}

// writeFn stands in for the write of a component. It receives the context and
// the indexing options the runtime hands to the component.
type writeFn func(ctx context.Context, opts compsearch.IndexingOptions) error

// policySearch is a search component whose writes are scripted, to observe how
// the runtime invokes them under a resiliency policy.
type policySearch struct {
	fakeSearch
	calls atomic.Int32
	write writeFn
}

func (p *policySearch) IndexDocuments(ctx context.Context, req *compsearch.IndexDocumentsRequest) (*compsearch.IndexDocumentsResponse, error) {
	p.calls.Add(1)
	if err := p.write(ctx, req.Options); err != nil {
		return nil, err
	}
	return &compsearch.IndexDocumentsResponse{Ack: compsearch.IndexAckCompleted}, nil
}

func (p *policySearch) DeleteDocuments(ctx context.Context, req *compsearch.DeleteDocumentsRequest) (*compsearch.DeleteDocumentsResponse, error) {
	p.calls.Add(1)
	if err := p.write(ctx, req.Options); err != nil {
		return nil, err
	}
	return &compsearch.DeleteDocumentsResponse{Ack: compsearch.IndexAckCompleted}, nil
}

// policyVector is the vector counterpart of policySearch.
type policyVector struct {
	fakeVector
	calls atomic.Int32
	write writeFn
}

func (p *policyVector) Upsert(ctx context.Context, req *compvector.UpsertRequest) (*compvector.UpsertResponse, error) {
	p.calls.Add(1)
	if err := p.write(ctx, req.Options); err != nil {
		return nil, err
	}
	return &compvector.UpsertResponse{Ack: compsearch.IndexAckCompleted}, nil
}

func (p *policyVector) Delete(ctx context.Context, req *compvector.DeleteRequest) (*compvector.DeleteResponse, error) {
	p.calls.Add(1)
	if err := p.write(ctx, req.Options); err != nil {
		return nil, err
	}
	return &compvector.DeleteResponse{Ack: compsearch.IndexAckCompleted}, nil
}

func waitForCompletion(wait time.Duration) *runtimev1pb.IndexingOptionsAlpha1 {
	return &runtimev1pb.IndexingOptionsAlpha1{
		Mode:          runtimev1pb.IndexingMode_INDEXING_MODE_WAIT_FOR_COMPLETION,
		WaitTimeout:   durationpb.New(wait),
		OnWaitTimeout: runtimev1pb.IndexingWaitTimeoutAction_INDEXING_WAIT_TIMEOUT_ACTION_FAIL_REQUEST,
	}
}

// TestWritesWithResiliency covers the four writes that take
// IndexingOptionsAlpha1 when the store has a timeout and a retry policy.
func TestWritesWithResiliency(t *testing.T) {
	t.Parallel()

	// write invokes one write API with the given options against a component
	// scripted by fn. It returns the number of times the component was invoked,
	// the error code a runtime rejection carries and the API error.
	type write func(t *testing.T, opts *runtimev1pb.IndexingOptionsAlpha1, fn writeFn) (calls int32, invalidRequest string, err error)

	newSearch := func(fn writeFn) (*Universal, *policySearch) {
		comp := &policySearch{write: fn}
		store := compstore.New()
		store.AddSearch(testSearchStore, comp)
		return &Universal{logger: testLogger, resiliency: resiliency.FromConfigurations(testLogger, writeResiliency), compStore: store}, comp
	}
	newVector := func(fn writeFn) (*Universal, *policyVector) {
		comp := &policyVector{write: fn}
		store := compstore.New()
		store.AddVector(testVectorStore, comp)
		return &Universal{logger: testLogger, resiliency: resiliency.FromConfigurations(testLogger, writeResiliency), compStore: store}, comp
	}

	writes := map[string]write{
		"IndexDocumentsAlpha1": func(t *testing.T, opts *runtimev1pb.IndexingOptionsAlpha1, fn writeFn) (int32, string, error) {
			t.Helper()
			api, comp := newSearch(fn)
			_, err := api.IndexDocumentsAlpha1(t.Context(), &runtimev1pb.IndexDocumentsRequestAlpha1{
				StoreName: testSearchStore, Index: testIndex, Options: opts,
				Documents: []*runtimev1pb.SearchDocument{{Id: "a", Content: []byte(`{}`)}},
			})
			return comp.calls.Load(), errorcodes.SearchInvalidRequest.GrpcCode, err
		},
		"DeleteDocumentsAlpha1": func(t *testing.T, opts *runtimev1pb.IndexingOptionsAlpha1, fn writeFn) (int32, string, error) {
			t.Helper()
			api, comp := newSearch(fn)
			_, err := api.DeleteDocumentsAlpha1(t.Context(), &runtimev1pb.DeleteDocumentsRequestAlpha1{
				StoreName: testSearchStore, Index: testIndex, Options: opts, Ids: []string{"a"},
			})
			return comp.calls.Load(), errorcodes.SearchInvalidRequest.GrpcCode, err
		},
		"UpsertVectorsAlpha1": func(t *testing.T, opts *runtimev1pb.IndexingOptionsAlpha1, fn writeFn) (int32, string, error) {
			t.Helper()
			api, comp := newVector(fn)
			_, err := api.UpsertVectorsAlpha1(t.Context(), &runtimev1pb.UpsertVectorsRequestAlpha1{
				StoreName: testVectorStore, Collection: testCollection, Options: opts,
				Records: []*runtimev1pb.VectorRecord{{Id: "a", Values: []float32{0.1}}},
			})
			return comp.calls.Load(), errorcodes.VectorInvalidRequest.GrpcCode, err
		},
		"DeleteVectorsAlpha1": func(t *testing.T, opts *runtimev1pb.IndexingOptionsAlpha1, fn writeFn) (int32, string, error) {
			t.Helper()
			api, comp := newVector(fn)
			_, err := api.DeleteVectorsAlpha1(t.Context(), &runtimev1pb.DeleteVectorsRequestAlpha1{
				StoreName: testVectorStore, Collection: testCollection, Options: opts, Ids: []string{"a"},
			})
			return comp.calls.Load(), errorcodes.VectorInvalidRequest.GrpcCode, err
		},
	}

	// The component applies the same portable rules as the runtime, against
	// the context it is handed.
	validate := func(ctx context.Context, opts compsearch.IndexingOptions) error {
		return compsearch.ValidateIndexingOptions(ctx, opts, true)
	}

	for name, do := range writes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			t.Run("a wait the policy timeout cannot accommodate is rejected before the component", func(t *testing.T) {
				t.Parallel()

				for _, wait := range []time.Duration{writePolicyTimeout, 2 * writePolicyTimeout} {
					calls, invalidRequest, err := do(t, waitForCompletion(wait), validate)
					assert.Equal(t, codes.InvalidArgument, grpcstatus.Code(err))
					assert.Equal(t, invalidRequest, statusReason(t, err))
					assert.Equal(t, []string{"options.wait_timeout"}, fieldViolations(t, err))
					assert.Zero(t, calls, "an invalid request must not reach the component, let alone be retried")
				}
			})

			t.Run("a wait within the policy timeout reaches the component once", func(t *testing.T) {
				t.Parallel()

				var deadline atomic.Bool
				calls, _, err := do(t, waitForCompletion(writePolicyTimeout/2), func(ctx context.Context, opts compsearch.IndexingOptions) error {
					_, ok := ctx.Deadline()
					deadline.Store(ok)
					return validate(ctx, opts)
				})
				require.NoError(t, err)
				assert.Equal(t, int32(1), calls)
				assert.True(t, deadline.Load(), "the component runs under the policy timeout")
			})

			t.Run("a transient component failure is retried", func(t *testing.T) {
				t.Parallel()

				var attempts atomic.Int32
				calls, _, err := do(t, nil, func(context.Context, compsearch.IndexingOptions) error {
					if attempts.Add(1) == 1 {
						return grpcstatus.Error(codes.Unavailable, "try again")
					}
					return nil
				})
				require.NoError(t, err)
				assert.Equal(t, int32(2), calls)
			})

			t.Run("a failure that outlasts the retries keeps its canonical code", func(t *testing.T) {
				t.Parallel()

				calls, _, err := do(t, nil, func(context.Context, compsearch.IndexingOptions) error {
					return grpcstatus.Error(codes.Unavailable, "still down")
				})
				assert.Equal(t, codes.Unavailable, grpcstatus.Code(err))
				assert.Equal(t, int32(3), calls, "one attempt and two retries")
			})
		})
	}
}
