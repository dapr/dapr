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
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.placement.authorizer")

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
	clientID, err := a.getClientID(ctx)
	if err != nil {
		return err
	}

	if msg == nil {
		return status.Errorf(codes.InvalidArgument, "received nil host report")
	}

	if len(msg.GetAddress()) == 0 || len(msg.GetAppId()) == 0 || len(msg.GetNamespace()) == 0 {
		return status.Errorf(codes.InvalidArgument, "host address, app ID and namespace must be provided")
	}

	if clientID != nil {
		if err = a.matchID(msg, clientID); err != nil {
			return err
		}
	}

	return a.actorTypes(msg)
}

func (a *Authorizer) matchID(host *schedulerv1pb.ActorHost, clientID *spiffe.Parsed) error {
	if host.GetAppId() != clientID.AppID() {
		return status.Errorf(
			codes.PermissionDenied,
			"provided app ID %s doesn't match the one in the SPIFFE ID (%s)",
			host.GetAppId(), clientID.AppID(),
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

// actorTypes rejects reports claiming internal actor types
// (dapr.internal.<namespace>.<appid>.*) which do not belong to the reporting
// app.
func (a *Authorizer) actorTypes(msg *schedulerv1pb.ActorHost) error {
	const partDapr = "dapr"
	const partInternal = "internal"

	for _, actorType := range msg.GetActorTypes() {
		split := strings.Split(actorType, ".")
		if len(split) >= 2 && split[0] == partDapr && split[1] == partInternal {
			if len(split) < 4 || split[2] != msg.GetNamespace() || split[3] != msg.GetAppId() {
				return status.Errorf(
					codes.PermissionDenied,
					"actor type %s is not allowed for app ID %s in namespace %s",
					actorType, msg.GetAppId(), msg.GetNamespace(),
				)
			}
		}
	}

	return nil
}

func (a *Authorizer) getClientID(ctx context.Context) (*spiffe.Parsed, error) {
	if !a.sec.MTLSEnabled() {
		return nil, nil
	}

	clientID, ok, err := spiffe.FromGRPCContext(ctx)
	if err != nil || !ok {
		log.Debugf("failed to get client ID from context: err=%v, ok=%t", err, ok)
		return nil, status.Errorf(codes.Unauthenticated, "failed to get client ID from context")
	}

	return clientID, nil
}
