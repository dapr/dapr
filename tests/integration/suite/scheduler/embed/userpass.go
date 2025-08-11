/*
Copyright 2025 The Dapr Authors
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

package embed

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/etcd"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(userpass))
}

type userpass struct {
	scheduler *scheduler.Scheduler
}

func (u *userpass) Setup(t *testing.T) []framework.Option {
	etcd := etcd.New(t,
		etcd.WithUsername("my-username"),
		etcd.WithPassword("my-password"),
	)

	u.scheduler = scheduler.New(t,
		scheduler.WithEmbed(false),
		scheduler.WithClientEndpoints(etcd.Endpoints()...),
		scheduler.WithClientUsername("my-username"),
		scheduler.WithClientPassword("my-password"),
	)

	return []framework.Option{
		framework.WithProcesses(etcd, u.scheduler),
	}
}

func (u *userpass) Run(t *testing.T, ctx context.Context) {
	u.scheduler.WaitUntilRunning(t, ctx)

	client := u.scheduler.Client(t, ctx)

	_, err := client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "test",
		Job: &schedulerv1.Job{
			Schedule: ptr.Of("@every 20s"),
		},
		Metadata: &schedulerv1.JobMetadata{
			AppId:     "appid",
			Namespace: "namespace",
			Target: &schedulerv1.JobTargetMetadata{
				Type: new(schedulerv1.JobTargetMetadata_Job),
			},
		},
	})
	require.NoError(t, err)
}
