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
	"io"
	"testing"

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/state"
	compv1pb "github.com/dapr/dapr/pkg/proto/components/v1"
)

// component is an implementation of the state store pluggable component
// interface.
type component struct {
	impl state.Store
}

func newComponent(t *testing.T, opts options) *component {
	return &component{
		impl: opts.statestore,
	}
}

func (c *component) BulkDelete(ctx context.Context, req *compv1pb.BulkDeleteRequest) (*compv1pb.BulkDeleteResponse, error) {
	dr := make([]state.DeleteRequest, len(req.Items))
	for i, item := range req.Items {
		dr[i] = state.DeleteRequest{
			Key:      item.Key,
			ETag:     &item.GetEtag().Value,
			Metadata: item.Metadata,
			Options: state.DeleteStateOption{
				Concurrency: concurrencyOf(item.Options.Concurrency),
				Consistency: consistencyOf(item.Options.Consistency),
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
	gr := make([]state.GetRequest, len(req.Items))
	for i, item := range req.Items {
		gr[i] = state.GetRequest{
			Key:      item.Key,
			Metadata: item.Metadata,
			Options: state.GetStateOption{
				Consistency: consistencyOf(item.Consistency),
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

		gresp.Items = append(gresp.Items, gitem)
	}

	return &gresp, nil
}

func (c *component) BulkSet(ctx context.Context, req *compv1pb.BulkSetRequest) (*compv1pb.BulkSetResponse, error) {
	sr := make([]state.SetRequest, len(req.Items))
	for i, item := range req.Items {
		var etag *string
		if item.Etag != nil {
			etag = &item.GetEtag().Value
		}
		sr[i] = state.SetRequest{
			Key:      item.Key,
			Value:    item.Value,
			ETag:     etag,
			Metadata: item.Metadata,
			Options: state.SetStateOption{
				Concurrency: concurrencyOf(item.Options.Concurrency),
				Consistency: consistencyOf(item.Options.Consistency),
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
	var etag *string
	if req.Etag != nil && len(req.GetEtag().Value) > 0 {
		etag = &req.GetEtag().Value
	}
	err := c.impl.Delete(ctx, &state.DeleteRequest{
		Key:      req.Key,
		ETag:     etag,
		Metadata: req.Metadata,
		Options: state.DeleteStateOption{
			Concurrency: concurrencyOf(req.Options.Concurrency),
			Consistency: consistencyOf(req.Options.Consistency),
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
	resp, err := c.impl.Get(ctx, &state.GetRequest{
		Key:      req.Key,
		Metadata: req.Metadata,
		Options: state.GetStateOption{
			Consistency: consistencyOf(req.Consistency),
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
			Properties: req.GetMetadata().Properties,
		},
	})
}

func (c *component) Close() error {
	return c.impl.(io.Closer).Close()
}

func (c *component) Ping(ctx context.Context, req *compv1pb.PingRequest) (*compv1pb.PingResponse, error) {
	return new(compv1pb.PingResponse), nil
}

func (c *component) Set(ctx context.Context, req *compv1pb.SetRequest) (*compv1pb.SetResponse, error) {
	var etag *string
	if req.Etag != nil && len(req.GetEtag().Value) > 0 {
		etag = &req.GetEtag().Value
	}
	err := c.impl.Set(ctx, &state.SetRequest{
		Key:      req.Key,
		Value:    req.Value,
		Metadata: req.Metadata,
		ETag:     etag,
		Options: state.SetStateOption{
			Concurrency: concurrencyOf(req.Options.Concurrency),
			Consistency: consistencyOf(req.Options.Consistency),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error performing set: %s", err)
	}
	return new(compv1pb.SetResponse), nil
}

func (c *component) Transact(ctx context.Context, req *compv1pb.TransactionalStateRequest) (*compv1pb.TransactionalStateResponse, error) {
	var operations []state.TransactionalStateOperation
	for _, op := range req.Operations {
		switch v := op.Request.(type) {
		case *compv1pb.TransactionalStateOperation_Delete:
			delReq := state.DeleteRequest{
				Key:      v.Delete.Key,
				Metadata: v.Delete.Metadata,
				Options: state.DeleteStateOption{
					Concurrency: concurrencyOf(v.Delete.Options.Concurrency),
					Consistency: consistencyOf(v.Delete.Options.Consistency),
				},
			}
			if v.Delete.Etag != nil {
				delReq.ETag = &v.Delete.GetEtag().Value
			}
			operations = append(operations, delReq)

		case *compv1pb.TransactionalStateOperation_Set:
			setReq := state.SetRequest{
				Key:      v.Set.Key,
				Value:    v.Set.Value,
				Metadata: v.Set.Metadata,
				Options: state.SetStateOption{
					Concurrency: concurrencyOf(v.Set.Options.Concurrency),
					Consistency: consistencyOf(v.Set.Options.Consistency),
				},
			}
			if v.Set.Etag != nil && v.Set.Etag.Value != "" {
				setReq.ETag = &v.Set.GetEtag().Value
			}
		default:
			return nil, fmt.Errorf("unknown operation type %T", v)
		}
	}

	err := c.impl.(state.TransactionalStore).Multi(ctx, &state.TransactionalStateRequest{
		Operations: operations,
		Metadata:   req.Metadata,
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
