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

package quorum

import (
	"context"
	"fmt"
	"log"
	"testing"
	"time"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func init() {
	suite.Register(new(notls))
}

// notls tests scheduler can find quorum with tls disabled.
type notls struct {
	schedulers []*scheduler.Scheduler
}

func (n *notls) Setup(t *testing.T) []framework.Option {
	fp := util.ReservePorts(t, 6)

	opts := []scheduler.Option{
		scheduler.WithInitialCluster(fmt.Sprintf("scheduler1=http://localhost:%d,scheduler2=http://localhost:%d,scheduler3=http://localhost:%d", fp.Port(t, 0), fp.Port(t, 1), fp.Port(t, 2))),
		scheduler.WithInitialClusterPorts(fp.Port(t, 0), fp.Port(t, 1), fp.Port(t, 2)),
	}

	n.schedulers = []*scheduler.Scheduler{
		scheduler.New(t, append(opts, scheduler.WithID("scheduler1"), scheduler.WithEtcdClientPort(fp.Port(t, 3)))...),
		scheduler.New(t, append(opts, scheduler.WithID("scheduler2"), scheduler.WithEtcdClientPort(fp.Port(t, 4)))...),
		scheduler.New(t, append(opts, scheduler.WithID("scheduler3"), scheduler.WithEtcdClientPort(fp.Port(t, 5)))...),
	}

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(n.schedulers[0], n.schedulers[1], n.schedulers[2]),
	}
}

func (n *notls) Run(t *testing.T, ctx context.Context) {
	n.schedulers[0].WaitUntilRunning(t, ctx)
	n.schedulers[1].WaitUntilRunning(t, ctx)
	n.schedulers[2].WaitUntilRunning(t, ctx)

	time.Sleep(time.Second * 2)

	fmt.Println("CASSIE!!! ALL ARE RUNNING")

	// Randomly choose one scheduler
	//chosenScheduler := n.schedulers[rand.Intn(3)]
	chosenScheduler := n.schedulers[0] //hardcoded for now
	log.Printf("CASSIE: choose scheduler: %s", chosenScheduler.ID())

	host := chosenScheduler.Address()
	conn, err := grpc.DialContext(ctx, host, grpc.WithBlock(), grpc.WithReturnConnectionError(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	fmt.Println("CASSIE!!! no issue w/ first conn")

	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := schedulerv1pb.NewSchedulerClient(conn)

	jobName := "testJob"
	req := &schedulerv1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     jobName,
			Schedule: "@daily",
		},
		Namespace: "default",
		Metadata:  map[string]string{"app_id": "test"},
	}

	log.Println("CASSIE: before scheduling job")

	_, err = client.ScheduleJob(ctx, req)
	log.Printf("CASSIE: after scheduling job: %s", err)
	require.NoError(t, err)

	// Choose a different scheduler for GetJob
	//diffScheduler := n.schedulers[rand.Intn(3)]
	diffScheduler := n.schedulers[0] //hard coded for now
	log.Printf("CASSIE: diff scheduler: %s", diffScheduler.ID())

	diffHost := diffScheduler.Address()
	diffConn, diffErr := grpc.DialContext(ctx, diffHost, grpc.WithBlock(), grpc.WithReturnConnectionError(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, diffErr)
	t.Cleanup(func() { require.NoError(t, diffConn.Close()) })

	diffClient := schedulerv1pb.NewSchedulerClient(diffConn)

	log.Println("CASSIE: in if err == nil")

	// verify job was scheduled
	getJobReq := &schedulerv1pb.JobRequest{
		JobName: "testJob",
	}

	log.Println("CASSIE: before get job")

	job, getJobErr := diffClient.GetJob(ctx, getJobReq)
	log.Printf("CASSIE: after get job: %s", getJobErr)
	log.Printf("CASSIE: job: %s", job)
	log.Printf("CASSIE: return val: %v", assert.Equal(t, jobName, job.GetJob().GetName()))
	assert.Equal(t, jobName, job.GetJob().GetName())

	log.Println("CASSIE: after the eventually")

}
