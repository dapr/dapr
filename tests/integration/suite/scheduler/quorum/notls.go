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
	"math/rand"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
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
		scheduler.WithInitialCluster(fmt.Sprintf("scheduler0=http://localhost:%d,scheduler1=http://localhost:%d,scheduler2=http://localhost:%d", fp.Port(t, 0), fp.Port(t, 1), fp.Port(t, 2))),
		scheduler.WithInitialClusterPorts(fp.Port(t, 0), fp.Port(t, 1), fp.Port(t, 2)),
	}

	clientPorts := []string{
		"scheduler0=" + strconv.Itoa(fp.Port(t, 3)),
		"scheduler1=" + strconv.Itoa(fp.Port(t, 4)),
		"scheduler2=" + strconv.Itoa(fp.Port(t, 5)),
	}
	n.schedulers = []*scheduler.Scheduler{
		scheduler.New(t, append(opts, scheduler.WithID("scheduler0"), scheduler.WithEtcdClientPorts(clientPorts))...),
		scheduler.New(t, append(opts, scheduler.WithID("scheduler1"), scheduler.WithEtcdClientPorts(clientPorts))...),
		scheduler.New(t, append(opts, scheduler.WithID("scheduler2"), scheduler.WithEtcdClientPorts(clientPorts))...),
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

	// Schedule job to random scheduler instance
	chosenScheduler := n.schedulers[rand.Intn(3)]

	host := chosenScheduler.Address()
	conn, err := grpc.DialContext(ctx, host, grpc.WithBlock(), grpc.WithReturnConnectionError(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := schedulerv1pb.NewSchedulerClient(conn)

	jobName := "appID||testJob"
	req := &schedulerv1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     jobName,
			Schedule: "@every 1s",
		},
		Namespace: "default",
		Metadata:  map[string]string{"app_id": "test"},
	}

	_, err = client.ScheduleJob(ctx, req)
	require.NoError(t, err)

	// Keep this, I believe the record is added to the db at trigger time, so I need the sleep for the db check to pass
	time.Sleep(2 * time.Second) // allow time for the record to be added to the db

	chosenSchedulerPort := findSchedulerClientPort(chosenScheduler)
	require.NotEmptyf(t, chosenSchedulerPort, "chosenSchedulerPort should not be empty")

	etcdCmdOutput := executeEtcdCmd(t, chosenSchedulerPort)
	checkCmdOutputForAppID(t, etcdCmdOutput)

	// ensure data exists on ALL schedulers
	for i := 0; i < 3; i++ {
		diffScheduler := n.schedulers[i]

		diffSchedulerPort := findSchedulerClientPort(diffScheduler)
		require.NotEmptyf(t, chosenSchedulerPort, "diffSchedulerPort should not be empty")

		diffEtcdCmdOutput := executeEtcdCmd(t, diffSchedulerPort)
		checkCmdOutputForAppID(t, diffEtcdCmdOutput)
	}
}

func findSchedulerClientPort(s *scheduler.Scheduler) string {
	schedulerID := s.ID()
	for _, entry := range s.EtcdClientPort() {
		parts := strings.Split(entry, "=")
		if len(parts) == 2 && parts[0] == schedulerID {
			return parts[1]
		}
	}
	return ""
}

func executeEtcdCmd(t *testing.T, port string) string {
	cmd := exec.Command("etcdctl", "get", "", "--prefix", fmt.Sprintf("--endpoints=localhost:%s", port))

	output, err := cmd.CombinedOutput()
	require.NoError(t, err)

	return string(output[:])
}

func checkCmdOutputForAppID(t *testing.T, output string) {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "etcd_cron/appid__testjob") {
			require.True(t, true, "Output contains 'etcd_cron/appid__testjob'")
			return
		}
	}
	require.Fail(t, "Output does not contain 'etcd_cron/appid__testjob'")
}
