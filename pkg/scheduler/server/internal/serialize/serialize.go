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

package serialize

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/authz"
	"github.com/dapr/dapr/pkg/security"
)

type Options struct {
	Security security.Handler
}

type Serializer struct {
	authz *authz.Authz
}

type Request interface {
	GetName() string
	GetMetadata() *schedulerv1pb.JobMetadata
}

type Job struct {
	name string
	meta *anypb.Any
}

func New(opts Options) *Serializer {
	return &Serializer{
		authz: authz.New(authz.Options{
			Security: opts.Security,
		}),
	}
}

func (s *Serializer) FromRequest(ctx context.Context, req Request) (*Job, error) {
	if err := s.authz.Metadata(ctx, req.GetMetadata()); err != nil {
		return nil, err
	}

	meta, err := anypb.New(req.GetMetadata())
	if err != nil {
		return nil, err
	}

	name, err := buildJobName(req)
	if err != nil {
		return nil, err
	}

	return &Job{
		meta: meta,
		name: name,
	}, nil
}

func (s *Serializer) KeyFromMetadata(ctx context.Context, req *schedulerv1pb.JobMetadata, asPrefix bool) (string, error) {
	if err := s.authz.Metadata(ctx, req); err != nil {
		return "", err
	}

	var str string
	switch t := req.GetTarget(); t.GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Actor:
		actor := t.GetActor()
		str = joinStrings("actorreminder", req.GetNamespace(), actor.GetType())
		if len(actor.GetId()) > 0 {
			str = joinStrings(str, actor.GetId())
		}
	case *schedulerv1pb.JobTargetMetadata_Job:
		str = joinStrings("app", req.GetNamespace(), req.GetAppId())
	default:
		return "", fmt.Errorf("unknown job type: %v", t)
	}

	if !asPrefix {
		str += "||"
	}

	return str, nil
}

func (s *Serializer) FromWatch(stream schedulerv1pb.Scheduler_WatchJobsServer) (*schedulerv1pb.WatchJobsRequestInitial, error) {
	req, err := stream.Recv()
	if err != nil {
		return nil, err
	}

	initial := req.GetInitial()
	if initial == nil {
		return nil, errors.New("initial request is required on stream connection")
	}

	if err := s.authz.WatchInitial(stream.Context(), initial); err != nil {
		return nil, err
	}

	typesImpl := make(map[schedulerv1pb.JobTargetType]struct{})
	for _, t := range initial.GetAcceptJobTypes() {
		if _, ok := typesImpl[t]; ok {
			return nil, fmt.Errorf("duplicate target type specified: %s", t)
		}
		typesImpl[t] = struct{}{}
	}

	return initial, nil
}

func (j *Job) Name() string {
	return j.name
}

func (j *Job) Metadata() *anypb.Any {
	return j.meta
}

func buildJobName(req Request) (string, error) {
	name := req.GetName()
	meta := req.GetMetadata()

	switch t := meta.GetTarget(); t.GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Actor:
		actor := t.GetActor()
		return joinStrings("actorreminder", meta.GetNamespace(), actor.GetType(), actor.GetId(), name), nil
	case *schedulerv1pb.JobTargetMetadata_Job:
		return joinStrings("app", meta.GetNamespace(), meta.GetAppId(), name), nil
	default:
		return "", fmt.Errorf("unknown job type: %v", t)
	}
}

func joinStrings(ss ...string) string {
	return strings.Join(ss, "||")
}
