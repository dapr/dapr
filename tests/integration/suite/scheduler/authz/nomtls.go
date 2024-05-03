/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package authz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(nomtls))
}

// nomtls tests scheduler with tls disabled.
type nomtls struct {
	scheduler *scheduler.Scheduler
}

func (n *nomtls) Setup(t *testing.T) []framework.Option {
	n.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(n.scheduler),
	}
}

func (n *nomtls) Run(t *testing.T, ctx context.Context) {
	n.scheduler.WaitUntilRunning(t, ctx)

	conn, err := grpc.DialContext(ctx, n.scheduler.Address(), grpc.WithBlock(), grpc.WithReturnConnectionError(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := schedulerv1pb.NewSchedulerClient(conn)

	req := &schedulerv1pb.ScheduleJobRequest{
		Name: "testJob",
		Job: &schedulerv1pb.Job{
			Schedule: ptr.Of("@daily"),
		},
		Metadata: &schedulerv1pb.ScheduleJobMetadata{
			AppId:     "test",
			Namespace: "default",
			Type: &schedulerv1pb.ScheduleJobMetadataType{
				Type: &schedulerv1pb.ScheduleJobMetadataType_Job{
					Job: new(schedulerv1pb.ScheduleTypeJob),
				},
			},
		},
	}

	_, err = client.ScheduleJob(ctx, req)
	require.NoError(t, err)
}
