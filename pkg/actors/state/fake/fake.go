/*
Copyright 2024 The Dapr Authors
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

package fake

import (
	"context"

	"github.com/dapr/dapr/pkg/actors/api"
)

type Fake struct {
	getFn                         func(ctx context.Context, req *api.GetStateRequest) (*api.StateResponse, error)
	getBulkFn                     func(ctx context.Context, req *api.GetBulkStateRequest) (api.BulkStateResponse, error)
	transactionalStateOperationFn func(ctx context.Context, ignoreHosted bool, req *api.TransactionalRequest) error
}

func New() *Fake {
	return &Fake{
		getFn: func(ctx context.Context, req *api.GetStateRequest) (*api.StateResponse, error) {
			return nil, nil
		},
		getBulkFn: func(ctx context.Context, req *api.GetBulkStateRequest) (api.BulkStateResponse, error) {
			return api.BulkStateResponse{}, nil
		},
		transactionalStateOperationFn: func(ctx context.Context, ignoreHosted bool, req *api.TransactionalRequest) error {
			return nil
		},
	}
}

func (f *Fake) WithGetFn(fn func(ctx context.Context, req *api.GetStateRequest) (*api.StateResponse, error)) *Fake {
	f.getFn = fn
	return f
}

func (f *Fake) WithGetBulkFn(fn func(ctx context.Context, req *api.GetBulkStateRequest) (api.BulkStateResponse, error)) *Fake {
	f.getBulkFn = fn
	return f
}

func (f *Fake) WithTransactionalStateOperationFn(fn func(ctx context.Context, ignoreHosted bool, req *api.TransactionalRequest) error) *Fake {
	f.transactionalStateOperationFn = fn
	return f
}

func (f *Fake) Get(ctx context.Context, req *api.GetStateRequest) (*api.StateResponse, error) {
	return f.getFn(ctx, req)
}

func (f *Fake) GetBulk(ctx context.Context, req *api.GetBulkStateRequest) (api.BulkStateResponse, error) {
	return f.getBulkFn(ctx, req)
}

func (f *Fake) TransactionalStateOperation(ctx context.Context, ignoreHosted bool, req *api.TransactionalRequest) error {
	return f.transactionalStateOperationFn(ctx, ignoreHosted, req)
}
