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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security/fake"
)

func Test_buildJobName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		req     Request
		expName string
		expErr  bool
	}{
		"nil meta should return error": {
			req: &schedulerv1pb.ScheduleJobRequest{
				Name:     "test",
				Metadata: nil,
			},
			expName: "",
			expErr:  true,
		},
		"meta with nil target should return error": {
			req: &schedulerv1pb.ScheduleJobRequest{
				Name:     "test",
				Metadata: new(schedulerv1pb.JobMetadata),
			},
			expName: "",
			expErr:  true,
		},
		"job meta should return concatenated name": {
			req: &schedulerv1pb.ScheduleJobRequest{
				Name: "test",
				Metadata: &schedulerv1pb.JobMetadata{
					Namespace: "myns", AppId: "myapp",
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Job{
							Job: new(schedulerv1pb.TargetJob),
						},
					},
				},
			},
			expName: "app||myns||myapp||test",
			expErr:  false,
		},
		"nil job meta should return concatenated name": {
			req: &schedulerv1pb.ScheduleJobRequest{
				Name: "test",
				Metadata: &schedulerv1pb.JobMetadata{
					Namespace: "myns", AppId: "myapp",
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Job{
							Job: nil,
						},
					},
				},
			},
			expName: "app||myns||myapp||test",
			expErr:  false,
		},
		"actor meta should return concatenated name": {
			req: &schedulerv1pb.ScheduleJobRequest{
				Name: "test",
				Metadata: &schedulerv1pb.JobMetadata{
					Namespace: "myns", AppId: "myapp",
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Actor{
							Actor: &schedulerv1pb.TargetActorReminder{
								Type: "myactortype", Id: "myactorid",
							},
						},
					},
				},
			},
			expName: "actorreminder||myns||myactortype||myactorid||test",
			expErr:  false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := buildJobName(test.req)
			assert.Equal(t, test.expErr, err != nil)
			assert.Equal(t, test.expName, got)
		})
	}
}

func Test_KeyFromMetadata(t *testing.T) {
	t.Parallel()

	actorMeta := func(actorType, actorID string) *schedulerv1pb.JobMetadata {
		return &schedulerv1pb.JobMetadata{
			Namespace: "ns", AppId: "app",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{Type: actorType, Id: actorID},
				},
			},
		}
	}
	jobMeta := &schedulerv1pb.JobMetadata{
		Namespace: "ns", AppId: "app",
		Target: &schedulerv1pb.JobTargetMetadata{
			Type: &schedulerv1pb.JobTargetMetadata_Job{Job: new(schedulerv1pb.TargetJob)},
		},
	}

	tests := map[string]struct {
		meta     *schedulerv1pb.JobMetadata
		asPrefix bool
		exp      string
	}{
		"actor with id":              {meta: actorMeta("type", "id"), exp: "actorreminder||ns||type||id||"},
		"actor with id as prefix":    {meta: actorMeta("type", "id"), asPrefix: true, exp: "actorreminder||ns||type||id"},
		"actor without id":           {meta: actorMeta("type", ""), exp: "actorreminder||ns||type||"},
		"actor without id as prefix": {meta: actorMeta("type", ""), asPrefix: true, exp: "actorreminder||ns||type||"},
		"job":                        {meta: jobMeta, exp: "app||ns||app||"},
		"job as prefix":              {meta: jobMeta, asPrefix: true, exp: "app||ns||app"},
		"internal type without id":   {meta: actorMeta("dapr.internal.ns.app.workflow", ""), asPrefix: true, exp: "actorreminder||ns||dapr.internal.ns.app.workflow||"},
	}

	s := New(Options{Security: fake.New().WithMTLSEnabled(false)})
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := s.KeyFromMetadata(t.Context(), test.meta, test.asPrefix)
			require.NoError(t, err)
			assert.Equal(t, test.exp, got)
		})
	}

	t.Run("a type prefix query cannot reach another actor type's keys", func(t *testing.T) {
		t.Parallel()
		internal, err := buildJobName(&schedulerv1pb.ScheduleJobRequest{
			Name:     "new-event",
			Metadata: actorMeta("dapr.internal.ns.app.workflow", "instance1"),
		})
		require.NoError(t, err)
		assert.Equal(t, "actorreminder||ns||dapr.internal.ns.app.workflow||instance1||new-event", internal)

		prefix, err := s.KeyFromMetadata(t.Context(), actorMeta("dapr", ""), true)
		require.NoError(t, err)
		assert.False(t, strings.HasPrefix(internal, prefix), "prefix %q must not match %q", prefix, internal)

		prefix, err = s.KeyFromMetadata(t.Context(), actorMeta("dapr.internal.ns.app.workflow", "inst"), true)
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(internal, prefix), "an ID prefix on the exact type still matches")
	})
}
