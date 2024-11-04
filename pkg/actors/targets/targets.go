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
	"time"

	"github.com/dapr/dapr/pkg/actors/requestresponse"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

type Interface interface {
	InvokeMethod(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error)
	InvokeReminder(ctx context.Context, reminder *requestresponse.Reminder) error
	InvokeTimer(ctx context.Context, reminder *requestresponse.Reminder) error
	Deactivate(ctx context.Context) error
}

type Idlable interface {
	Interface
	Key() string
	ScheduledTime() time.Time
	IdleAt(time.Time)
	IsBusy() bool
	Channel() <-chan struct{}
}

type Factory = func(actorID string) Interface
