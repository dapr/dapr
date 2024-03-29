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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(notls))
}

// notls tests scheduler can find quorum with tls disabled.
type notls struct {
	schedulers []*scheduler.Scheduler

	jobName string
}

func (n *notls) Setup(t *testing.T) []framework.Option {
	n.jobName = uuid.NewString()

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
	//nolint:gosec // there is no need for a crypto secure rand.
	chosenScheduler := n.schedulers[rand.Intn(3)]

	host := chosenScheduler.Address()
	conn, err := grpc.DialContext(ctx, host, grpc.WithBlock(), grpc.WithReturnConnectionError(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := schedulerv1pb.NewSchedulerClient(conn)

	req := &schedulerv1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     n.jobName,
			Schedule: "@every 1s",
		},
		Metadata: map[string]string{
			"namespace": "default",
		},
	}

	_, err = client.ScheduleJob(ctx, req)
	require.NoError(t, err)

	// Keep this, I believe the record is added to the db at trigger time, so I need the sleep for the db check to pass
	time.Sleep(2 * time.Second) // allow time for the record to be added to the db

	chosenSchedulerPort := chosenScheduler.EtcdClientPort()
	require.NotEmptyf(t, chosenSchedulerPort, "chosenSchedulerPort should not be empty")

	chosenSchedulerEtcdKeys := getEtcdKeys(t, chosenSchedulerPort)
	checkKeysForAppID(t, n.jobName, chosenSchedulerEtcdKeys)

	// ensure data exists on ALL schedulers
	for i := 0; i < 3; i++ {
		diffScheduler := n.schedulers[i]

		diffSchedulerPort := diffScheduler.EtcdClientPort()
		require.NotEmptyf(t, diffSchedulerPort, "diffSchedulerPort should not be empty")

		diffSchedulerEtcdKeys := getEtcdKeys(t, diffSchedulerPort)
		checkKeysForAppID(t, n.jobName, diffSchedulerEtcdKeys)
	}
}

func checkKeysForAppID(t *testing.T, jobName string, keys []*mvccpb.KeyValue) {
	found := false
	for _, kv := range keys {
		if strings.HasSuffix(string(kv.Key), jobName) {
			found = true
			break
		}
	}
	require.True(t, found, "job's key not found: '%s'", jobName)
}

func getEtcdKeys(t *testing.T, port string) []*mvccpb.KeyValue {
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("localhost:%s", port)},
		DialTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Get keys with prefix
	resp, err := client.Get(ctx, "", clientv3.WithPrefix())
	require.NoError(t, err)

	return resp.Kvs
}
