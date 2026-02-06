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
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.placement.authorizer")

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
	clientID, err := a.getClientID(stream)
	if err != nil {
		return err
	}

	if msg == nil {
		return status.Errorf(codes.InvalidArgument, "received nil host report")
	}

	if msg.GetOperation() == v1pb.HostOperation_UNKNOWN && msg.Version != nil {
		return status.Errorf(codes.InvalidArgument, "both operation and version must be set or both must be unset")
	}

	if len(msg.GetId()) == 0 || len(msg.GetNamespace()) == 0 {
		return status.Errorf(codes.InvalidArgument, "host ID and namespace must be provided")
	}

	// Ensure the provided host ID and namespace match those in the client SPIFFE
	// ID.
	if clientID != nil {
		if err = a.matchID(msg, clientID); err != nil {
			return err
		}
	}

	if err = a.actorTypes(msg); err != nil {
		return err
	}

	return nil
}

func (a *Authorizer) matchID(host *v1pb.Host, clientID *spiffe.Parsed) error {
	if host.GetId() != clientID.AppID() {
		return status.Errorf(
			codes.PermissionDenied,
			"provided app ID %s doesn't match the one in the SPIFFE ID (%s)",
			host.GetId(), clientID.AppID(),
		)
	}

	if host.GetNamespace() != clientID.Namespace() {
		return status.Errorf(
			codes.PermissionDenied,
			"provided client namespace %s doesn't match the one in the SPIFFE ID (%s)",
			host.GetNamespace(), clientID.Namespace(),
		)
	}

	return nil
}

func (a *Authorizer) actorTypes(msg *v1pb.Host) error {
	const partDapr = "dapr"
	const partInternal = "internal"

	for _, entity := range msg.GetEntities() {
		split := strings.Split(entity, ".")
		if len(split) >= 2 && split[0] == partDapr && split[1] == partInternal {
			if len(split) < 4 || split[2] != msg.GetNamespace() || split[3] != msg.GetId() {
				return status.Errorf(
					codes.PermissionDenied,
					"entity %s is not allowed for app ID %s in namespace %s",
					entity, msg.GetId(), msg.GetNamespace(),
				)
			}
		}
	}

	return nil
}

func (a *Authorizer) getClientID(stream v1pb.Placement_ReportDaprStatusServer) (*spiffe.Parsed, error) {
	if !a.sec.MTLSEnabled() {
		return nil, nil
	}

	clientID, ok, err := spiffe.FromGRPCContext(stream.Context())
	if err != nil || !ok {
		log.Debugf("failed to get client ID from context: err=%v, ok=%t", err, ok)
		return nil, status.Errorf(codes.Unauthenticated, "failed to get client ID from context")
	}

	return clientID, nil
}
