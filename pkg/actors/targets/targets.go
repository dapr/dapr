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

package targets

import (
	"context"

	"github.com/dapr/dapr/pkg/actors/api"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

type Interface interface {
	Key() string
	Type() string
	ID() string

	InvokeMethod(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	InvokeReminder(ctx context.Context, reminder *api.Reminder) error
	InvokeTimer(ctx context.Context, reminder *api.Reminder) error
	InvokeStream(ctx context.Context, req *internalv1pb.InternalInvokeRequest, stream func(*internalv1pb.InternalInvokeResponse) (bool, error)) error
	Deactivate(context.Context) error
}

type Factory interface {
	GetOrCreate(string) Interface
	Exists(string) bool
	HaltAll(context.Context) error
	HaltNonHosted(context.Context) error
	Len() int
}
