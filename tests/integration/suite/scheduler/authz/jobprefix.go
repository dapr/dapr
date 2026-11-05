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

package authz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

const jobPrefixNamespace = "default"

func init() {
	suite.Register(new(jobprefix))
}

// jobprefix asserts a DeleteByMetadata with id_prefix_match on a Job target
// only deletes the caller's own jobs, not those of other apps in the same
// namespace whose app ID starts with the caller's.
type jobprefix struct {
	sentry    *sentry.Sentry
	scheduler *scheduler.Scheduler
}

func (j *jobprefix) Setup(t *testing.T) []framework.Option {
	j.sentry = sentry.New(t)
	j.scheduler = scheduler.New(t,
		scheduler.WithSentry(j.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
	)

	return []framework.Option{
		framework.WithProcesses(j.sentry, j.scheduler),
	}
}

func (j *jobprefix) Run(t *testing.T, ctx context.Context) {
	j.sentry.WaitUntilRunning(t, ctx)
	j.scheduler.WaitUntilRunning(t, ctx)

	meta := func(appID string) *schedulerv1pb.JobMetadata {
		return &schedulerv1pb.JobMetadata{
			AppId:     appID,
			Namespace: jobPrefixNamespace,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{Job: new(schedulerv1pb.TargetJob)},
			},
		}
	}

	clients := make(map[string]schedulerv1pb.SchedulerClient)
	for _, appID := range []string{"app", "app2", "app-payments"} {
		clients[appID] = j.scheduler.ClientMTLS(t, ctx, appID)
		_, err := clients[appID].ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name:     "job1",
			Job:      &schedulerv1pb.Job{DueTime: new("1000s")},
			Metadata: meta(appID),
		})
		require.NoError(t, err)
	}

	_, err := clients["app"].DeleteByMetadata(ctx, &schedulerv1pb.DeleteByMetadataRequest{
		IdPrefixMatch: new(true),
		Metadata:      meta("app"),
	})
	require.NoError(t, err)

	for appID, exp := range map[string]int{"app": 0, "app2": 1, "app-payments": 1} {
		resp, err := clients[appID].ListJobs(ctx, &schedulerv1pb.ListJobsRequest{Metadata: meta(appID)})
		require.NoError(t, err)
		assert.Len(t, resp.GetJobs(), exp, "jobs of app %s", appID)
	}
}
