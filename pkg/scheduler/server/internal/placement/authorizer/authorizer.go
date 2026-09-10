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

package authorizer

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/actors/hostauthz"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
)

type Options struct {
	Security security.Handler
}

type Authorizer struct {
	sec security.Handler
}

func New(opts Options) *Authorizer {
	return &Authorizer{
		sec: opts.Security,
	}
}

// Host authorizes an ActorHost report against the stream's SPIFFE identity.
func (a *Authorizer) Host(ctx context.Context, msg *schedulerv1pb.ActorHost) error {
	if msg == nil {
		return status.Errorf(codes.InvalidArgument, "received nil host report")
	}

	if len(msg.GetAddress()) == 0 || len(msg.GetAppId()) == 0 || len(msg.GetNamespace()) == 0 {
		return status.Errorf(codes.InvalidArgument, "host address, app ID and namespace must be provided")
	}

	return hostauthz.Authorize(ctx, a.sec, hostauthz.Report{
		AppID:      msg.GetAppId(),
		Namespace:  msg.GetNamespace(),
		ActorTypes: msg.GetActorTypes(),
	})
}
