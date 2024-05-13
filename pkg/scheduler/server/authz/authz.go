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

package authz

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.authz")

type Options struct {
	Security security.Handler
}

type Authz struct {
	sec security.Handler
}

func New(opts Options) *Authz {
	return &Authz{sec: opts.Security}
}

func (a *Authz) Metadata(ctx context.Context, meta *schedulerv1pb.ScheduleJobMetadata) error {
	return a.authz(ctx, meta.GetNamespace(), meta.GetAppId())
}

func (a *Authz) Initial(ctx context.Context, initial *schedulerv1pb.WatchJobsRequestInitial) error {
	return a.authz(ctx, initial.GetNamespace(), initial.GetAppId())
}

func (a *Authz) authz(ctx context.Context, ns, appID string) error {
	if len(ns) == 0 || len(appID) == 0 {
		log.Debugf("missing namespace or appID in metadata: ns=%s, appID=%s", ns, appID)
		return status.Errorf(codes.InvalidArgument, "missing namespace or appID in request")
	}

	if !a.sec.MTLSEnabled() {
		return nil
	}

	id, ok, err := spiffe.FromGRPCContext(ctx)
	if err != nil || !ok {
		log.Debugf("failed to get identity from context: err=%v, ok=%t", err, ok)
		return status.Errorf(codes.Unauthenticated, "failed to get identity from context")
	}

	if id.Namespace() != ns || id.AppID() != appID {
		log.Debugf("identity does not match metadata: client=%s, req=%s/%s", id, ns, appID)
		return status.Errorf(codes.PermissionDenied, "identity does not match request")
	}

	return nil
}
