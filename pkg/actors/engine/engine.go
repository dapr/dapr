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

package engine

import (
	"context"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/engine/internal/api"
	"github.com/dapr/dapr/pkg/actors/engine/internal/queue"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

type Interface interface {
	Run(ctx context.Context) error

	Call(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	CallReminder(ctx context.Context, reminder *actorsapi.Reminder) error
	CallStream(ctx context.Context, req *internalv1pb.InternalInvokeRequest, stream chan<- *internalv1pb.InternalInvokeResponse) error
}

type Options = api.Options

func New(opts Options) Interface {
	return queue.New(api.New(opts))
}
