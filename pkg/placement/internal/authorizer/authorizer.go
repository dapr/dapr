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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/actors/hostauthz"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
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

func (a *Authorizer) Host(stream v1pb.Placement_ReportDaprStatusServer, msg *v1pb.Host) error {
	if msg == nil {
		return status.Errorf(codes.InvalidArgument, "received nil host report")
	}

	if msg.GetOperation() == v1pb.HostOperation_UNKNOWN && msg.Version != nil {
		return status.Errorf(codes.InvalidArgument, "both operation and version must be set or both must be unset")
	}

	if len(msg.GetId()) == 0 || len(msg.GetNamespace()) == 0 {
		return status.Errorf(codes.InvalidArgument, "host ID and namespace must be provided")
	}

	return hostauthz.Authorize(stream.Context(), a.sec, hostauthz.Report{
		AppID:      msg.GetId(),
		Namespace:  msg.GetNamespace(),
		ActorTypes: msg.GetEntities(),
	})
}
