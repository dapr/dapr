/*
Copyright 2023 The Dapr Authors
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

package statestore

import (
	"context"
	"fmt"
	"testing"

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/state"
	compv1pb "github.com/dapr/dapr/pkg/proto/components/v1"
)

// component is an implementation of the state store pluggable component
// interface.
type component struct {
	impl state.Store

	getErr        error
	bulkGetErr    error
	bulkDeleteErr error
	queryErr      error
	bulkSetErr    error
	deleteErr     error
	setErr        error
}

func newComponent(t *testing.T, opts options) *component {
	return &component{
		impl:          opts.statestore,
		getErr:        opts.getErr,
		bulkGetErr:    opts.bulkGetErr,
		bulkDeleteErr: opts.bulkDeleteErr,
		queryErr:      opts.queryErr,
		bulkSetErr:    opts.bulkSetErr,
		deleteErr:     opts.deleteErr,
		setErr:        opts.setErr,
	}
}

func (c *component) BulkDelete(ctx context.Context, req *compv1pb.BulkDeleteRequest) (*compv1pb.BulkDeleteResponse, error) {
	if c.bulkDeleteErr != nil {
		return nil, c.bulkDeleteErr
	}

	dr := make([]state.DeleteRequest, len(req.GetItems()))
	for i, item := range req.GetItems() {
		dr[i] = state.DeleteRequest{
			Key:      item.GetKey(),
			ETag:     &item.GetEtag().Value,
			Metadata: item.GetMetadata(),
			Options: state.DeleteStateOption{
				Concurrency: concurrencyOf(item.GetOptions().GetConcurrency()),
				Consistency: consistencyOf(item.GetOptions().GetConsistency()),
			},
		}
	}

	err := c.impl.BulkDelete(ctx, dr, state.BulkStoreOpts{})
	if err != nil {
		return nil, fmt.Errorf("error performing bulk delete: %s", err)
	}

	return new(compv1pb.BulkDeleteResponse), nil
}

func (c *component) BulkGet(ctx context.Context, req *compv1pb.BulkGetRequest) (*compv1pb.BulkGetResponse, error) {
	if c.bulkGetErr != nil {
		return nil, c.bulkGetErr
	}

	gr := make([]state.GetRequest, len(req.GetItems()))
	for i, item := range req.GetItems() {
		gr[i] = state.GetRequest{
			Key:      item.GetKey(),
			Metadata: item.GetMetadata(),
			Options: state.GetStateOption{
				Consistency: consistencyOf(item.GetConsistency()),
			},
		}
	}

	resp, err := c.impl.BulkGet(ctx, gr, state.BulkGetOpts{})
	if err != nil {
		return nil, fmt.Errorf("error performing bulk get: %s", err)
	}

	var gresp compv1pb.BulkGetResponse
	for _, item := range resp {
		gitem := &compv1pb.BulkStateItem{
			Key:      item.Key,
			Data:     item.Data,
			Metadata: item.Metadata,
		}

		if item.ETag != nil {
			gitem.Etag = &compv1pb.Etag{
				Value: *item.ETag,
			}
		}

		gresp.Items = append(gresp.GetItems(), gitem)
	}

	return &gresp, nil
}

func (c *component) Query(ctx context.Context, req *compv1pb.QueryRequest) (*compv1pb.QueryResponse, error) {
	if c.queryErr != nil {
		return nil, c.queryErr
	}

	// TODO: @joshvanl implement query API transformation, rather than just
	// sending nothing and returning error.
	_, err := c.impl.(state.Querier).Query(ctx, &state.QueryRequest{})
	return nil, err
}

func (c *component) MultiMaxSize(context.Context, *compv1pb.MultiMaxSizeRequest) (*compv1pb.MultiMaxSizeResponse, error) {
	return &compv1pb.MultiMaxSizeResponse{
		MaxSize: int64(c.impl.(state.TransactionalStoreMultiMaxSize).MultiMaxSize()),
	}, nil
}

func (c *component) BulkSet(ctx context.Context, req *compv1pb.BulkSetRequest) (*compv1pb.BulkSetResponse, error) {
	if c.bulkSetErr != nil {
		return nil, c.bulkSetErr
	}

	sr := make([]state.SetRequest, len(req.GetItems()))
	for i, item := range req.GetItems() {
		var etag *string
		if item.GetEtag() != nil {
			etag = &item.GetEtag().Value
		}
		sr[i] = state.SetRequest{
			Key:      item.GetKey(),
			Value:    item.GetValue(),
			ETag:     etag,
			Metadata: item.GetMetadata(),
			Options: state.SetStateOption{
				Concurrency: concurrencyOf(item.GetOptions().GetConcurrency()),
				Consistency: consistencyOf(item.GetOptions().GetConsistency()),
			},
		}
	}

	err := c.impl.BulkSet(ctx, sr, state.BulkStoreOpts{})
	if err != nil {
		return nil, fmt.Errorf("error performing bulk set: %s", err)
	}

	return new(compv1pb.BulkSetResponse), nil
}

func (c *component) Delete(ctx context.Context, req *compv1pb.DeleteRequest) (*compv1pb.DeleteResponse, error) {
	if c.deleteErr != nil {
		return nil, c.deleteErr
	}

	var etag *string
	if req.GetEtag() != nil && len(req.GetEtag().GetValue()) > 0 {
		etag = &req.GetEtag().Value
	}
	err := c.impl.Delete(ctx, &state.DeleteRequest{
		Key:      req.GetKey(),
		ETag:     etag,
		Metadata: req.GetMetadata(),
		Options: state.DeleteStateOption{
			Concurrency: concurrencyOf(req.GetOptions().GetConcurrency()),
			Consistency: consistencyOf(req.GetOptions().GetConsistency()),
		},
	})
	if err != nil {
		return nil, err
	}
	return new(compv1pb.DeleteResponse), nil
}

func (c *component) Features(context.Context, *compv1pb.FeaturesRequest) (*compv1pb.FeaturesResponse, error) {
	implF := c.impl.Features()
	features := make([]string, len(implF))
	for i, f := range implF {
		features[i] = string(f)
	}
	return &compv1pb.FeaturesResponse{
		Features: features,
	}, nil
}

func (c *component) Get(ctx context.Context, req *compv1pb.GetRequest) (*compv1pb.GetResponse, error) {
	if c.getErr != nil {
		return nil, c.getErr
	}

	resp, err := c.impl.Get(ctx, &state.GetRequest{
		Key:      req.GetKey(),
		Metadata: req.GetMetadata(),
		Options: state.GetStateOption{
			Consistency: consistencyOf(req.GetConsistency()),
		},
	})
	if err != nil {
		return nil, err
	}

	gresp := compv1pb.GetResponse{
		Data:     resp.Data,
		Metadata: resp.Metadata,
	}

	if resp.ETag != nil && len(*resp.ETag) > 0 {
		gresp.Etag = &compv1pb.Etag{
			Value: *resp.ETag,
		}
	}

	return &gresp, nil
}

func (c *component) Init(ctx context.Context, req *compv1pb.InitRequest) (*compv1pb.InitResponse, error) {
	return new(compv1pb.InitResponse), c.impl.Init(ctx, state.Metadata{
		Base: metadata.Base{
			Name:       "state.wrapped-in-memory",
			Properties: req.GetMetadata().GetProperties(),
		},
	})
}

func (c *component) Close() error {
	return c.impl.Close()
}

func (c *component) Ping(ctx context.Context, req *compv1pb.PingRequest) (*compv1pb.PingResponse, error) {
	return new(compv1pb.PingResponse), nil
}

func (c *component) Set(ctx context.Context, req *compv1pb.SetRequest) (*compv1pb.SetResponse, error) {
	if c.setErr != nil {
		return nil, c.setErr
	}

	var etag *string
	if req.GetEtag() != nil && len(req.GetEtag().GetValue()) > 0 {
		etag = &req.GetEtag().Value
	}
	err := c.impl.Set(ctx, &state.SetRequest{
		Key:      req.GetKey(),
		Value:    req.GetValue(),
		Metadata: req.GetMetadata(),
		ETag:     etag,
		Options: state.SetStateOption{
			Concurrency: concurrencyOf(req.GetOptions().GetConcurrency()),
			Consistency: consistencyOf(req.GetOptions().GetConsistency()),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error performing set: %s", err)
	}
	return new(compv1pb.SetResponse), nil
}

func (c *component) Transact(ctx context.Context, req *compv1pb.TransactionalStateRequest) (*compv1pb.TransactionalStateResponse, error) {
	var operations []state.TransactionalStateOperation
	for _, op := range req.GetOperations() {
		switch v := op.GetRequest().(type) {
		case *compv1pb.TransactionalStateOperation_Delete:
			delReq := state.DeleteRequest{
				Key:      v.Delete.GetKey(),
				Metadata: v.Delete.GetMetadata(),
				Options: state.DeleteStateOption{
					Concurrency: concurrencyOf(v.Delete.GetOptions().GetConcurrency()),
					Consistency: consistencyOf(v.Delete.GetOptions().GetConsistency()),
				},
			}
			if v.Delete.GetEtag() != nil {
				delReq.ETag = &v.Delete.GetEtag().Value
			}
			operations = append(operations, delReq)

		case *compv1pb.TransactionalStateOperation_Set:
			setReq := state.SetRequest{
				Key:      v.Set.GetKey(),
				Value:    v.Set.GetValue(),
				Metadata: v.Set.GetMetadata(),
				Options: state.SetStateOption{
					Concurrency: concurrencyOf(v.Set.GetOptions().GetConcurrency()),
					Consistency: consistencyOf(v.Set.GetOptions().GetConsistency()),
				},
			}
			if v.Set.GetEtag() != nil && v.Set.GetEtag().GetValue() != "" {
				setReq.ETag = &v.Set.GetEtag().Value
			}
			operations = append(operations, setReq)
		default:
			return nil, fmt.Errorf("unknown operation type %T", v)
		}
	}

	err := c.impl.(state.TransactionalStore).Multi(ctx, &state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   req.GetMetadata(),
	})
	if err != nil {
		return nil, fmt.Errorf("error performing transactional state operation: %s", err)
	}
	return new(compv1pb.TransactionalStateResponse), nil
}

var consistencyModels = map[compv1pb.StateOptions_StateConsistency]string{
	compv1pb.StateOptions_CONSISTENCY_EVENTUAL: state.Eventual,
	compv1pb.StateOptions_CONSISTENCY_STRONG:   state.Strong,
}

func consistencyOf(value compv1pb.StateOptions_StateConsistency) string {
	consistency, ok := consistencyModels[value]
	if !ok {
		return ""
	}
	return consistency
}

var concurrencyModels = map[compv1pb.StateOptions_StateConcurrency]string{
	compv1pb.StateOptions_CONCURRENCY_FIRST_WRITE: state.FirstWrite,
	compv1pb.StateOptions_CONCURRENCY_LAST_WRITE:  state.LastWrite,
}

func concurrencyOf(value compv1pb.StateOptions_StateConcurrency) string {
	concurrency, ok := concurrencyModels[value]
	if !ok {
		return ""
	}
	return concurrency
}
