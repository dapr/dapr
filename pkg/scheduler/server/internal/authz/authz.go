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
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/actors/hostauthz"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
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

// Request is a named job request.
type Request interface {
	GetName() string
	GetMetadata() *schedulerv1pb.JobMetadata
}

func (a *Authz) Metadata(ctx context.Context, meta *schedulerv1pb.JobMetadata) error {
	return a.job(ctx, "", meta, false)
}

// Job authorizes a request for a named job. An actor targeted job on a
// reserved internal actor type (dapr.internal.<namespace>.<appid>.*) must
// belong to the requesting app. The one exception is scheduling, without
// overwrite, an activity result reminder on a workflow actor in the same
// namespace, which is how an activity host delivers a result to the
// workflow's app: reading, deleting or replacing such a job stays with the
// owner.
func (a *Authz) Job(ctx context.Context, req Request) error {
	schedule, ok := req.(*schedulerv1pb.ScheduleJobRequest)
	return a.job(ctx, req.GetName(), req.GetMetadata(), ok && !schedule.GetOverwrite())
}

func (a *Authz) job(ctx context.Context, name string, meta *schedulerv1pb.JobMetadata, createOnly bool) error {
	if err := a.authz(ctx, meta.GetNamespace(), meta.GetAppId()); err != nil {
		return err
	}

	actor := meta.GetTarget().GetActor()
	if actor == nil {
		return nil
	}

	internal, owned := hostauthz.InternalActorType(actor.GetType(), meta.GetNamespace(), meta.GetAppId())
	if !internal || owned {
		return nil
	}

	activityResult := createOnly &&
		strings.HasPrefix(name, common.ReminderPrefixActivityResult) &&
		strings.HasPrefix(actor.GetType(), "dapr.internal."+meta.GetNamespace()+".") &&
		strings.HasSuffix(actor.GetType(), ".workflow")
	if !activityResult {
		log.Debugf("internal actor type does not belong to app: type=%s, req=%s/%s", actor.GetType(), meta.GetNamespace(), meta.GetAppId())
		monitoring.RecordSidecarAuthError()
		return status.Errorf(codes.PermissionDenied, "actor type %s is not allowed for app ID %s in namespace %s", actor.GetType(), meta.GetAppId(), meta.GetNamespace())
	}

	return nil
}

func (a *Authz) WatchInitial(ctx context.Context, initial *schedulerv1pb.WatchJobsRequestInitial) error {
	return a.authz(ctx, initial.GetNamespace(), initial.GetAppId())
}

func (a *Authz) authz(ctx context.Context, ns, appID string) error {
	if len(ns) == 0 || len(appID) == 0 {
		log.Debugf("missing namespace or appID in metadata: ns=%s, appID=%s", ns, appID)
		monitoring.RecordSidecarAuthError()
		return status.Errorf(codes.InvalidArgument, "missing namespace or appID in request")
	}

	if !a.sec.MTLSEnabled() {
		return nil
	}

	id, ok, err := spiffe.FromGRPCContext(ctx)
	if err != nil || !ok {
		log.Debugf("failed to get identity from context: err=%v, ok=%t", err, ok)
		monitoring.RecordSidecarAuthError()
		return status.Errorf(codes.Unauthenticated, "failed to get identity from context")
	}

	if id.Namespace() != ns || id.AppID() != appID {
		log.Debugf("identity does not match metadata: client=%v, req=%s/%s", id, ns, appID)
		monitoring.RecordSidecarAuthError()
		return status.Errorf(codes.PermissionDenied, "identity does not match request")
	}

	return nil
}
