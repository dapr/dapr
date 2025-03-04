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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(notls))
}

// notls tests scheduler can find quorum with tls disabled.
type notls struct {
	schedulers []*scheduler.Scheduler
}

func (n *notls) Setup(t *testing.T) []framework.Option {
	fp := ports.Reserve(t, 6)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	port4, port5, port6 := fp.Port(t), fp.Port(t), fp.Port(t)

	opts := []scheduler.Option{
		scheduler.WithInitialCluster(fmt.Sprintf(
			"scheduler-0=http://127.0.0.1:%d,scheduler-1=http://127.0.0.1:%d,scheduler-2=http://127.0.0.1:%d",
			port1, port2, port3),
		),
	}

	n.schedulers = []*scheduler.Scheduler{
		scheduler.New(t, append(opts, scheduler.WithID("scheduler-0"), scheduler.WithEtcdClientPort(port4))...),
		scheduler.New(t, append(opts, scheduler.WithID("scheduler-1"), scheduler.WithEtcdClientPort(port5))...),
		scheduler.New(t, append(opts, scheduler.WithID("scheduler-2"), scheduler.WithEtcdClientPort(port6))...),
	}

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(fp, n.schedulers[0], n.schedulers[1], n.schedulers[2]),
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
	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, host, grpc.WithBlock(), grpc.WithReturnConnectionError(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := schedulerv1pb.NewSchedulerClient(conn)

	req := &schedulerv1pb.ScheduleJobRequest{
		Name: "testJob",
		Job: &schedulerv1pb.Job{
			// Set to 90 so the job doesn't get cleaned up before I check for it in etcd
			// also so the job doesn't reach daprd to be sent to an app bc there is not app
			// for this test
			Schedule: ptr.Of("@every 90s"),
			Repeats:  ptr.Of(uint32(1)),
			Data: &anypb.Any{
				TypeUrl: "type.googleapis.com/google.type.Expr",
			},
			Ttl: ptr.Of("90s"),
		},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     "appid",
			Namespace: "ns",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	}

	_, err = client.ScheduleJob(ctx, req)
	require.NoError(t, err)

	chosenSchedulerPort := chosenScheduler.EtcdClientPort()
	require.NotEmptyf(t, chosenSchedulerPort, "chosenSchedulerPort should not be empty")

	// Check if the job's key exists in the etcd database
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		chosenSchedulerEtcdKeys := getEtcdKeys(t, chosenSchedulerPort)
		n.checkKeysForJobName(t, "testJob", chosenSchedulerEtcdKeys)
	}, time.Second*40, time.Millisecond*10, "failed to find job's key in etcd")

	// ensure data exists on ALL schedulers
	for i := range n.schedulers {
		diffScheduler := n.schedulers[i]

		diffSchedulerPort := diffScheduler.EtcdClientPort()
		require.NotEmptyf(t, diffSchedulerPort, "diffSchedulerPort should not be empty")

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			diffSchedulerEtcdKeys := getEtcdKeys(t, diffSchedulerPort)
			n.checkKeysForJobName(t, "testJob", diffSchedulerEtcdKeys)
		}, time.Second*40, time.Millisecond*10, "failed to find job's key in etcd")
	}
}

func (n *notls) checkKeysForJobName(t *testing.T, jobName string, keys []*mvccpb.KeyValue) {
	t.Helper()

	// should have the same path separator across OS
	jobPrefix := "dapr/jobs/app"
	found := false
	for _, kv := range keys {
		if string(kv.Key) == fmt.Sprintf("%s||%s||%s||%s", jobPrefix, "ns", "appid", jobName) {
			found = true
			break
		}
	}
	require.True(t, found, "job's key not found: '%s'", jobName)
}

func getEtcdKeys(t *testing.T, port int) []*mvccpb.KeyValue {
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"127.0.0.1:" + strconv.Itoa(port)},
		DialTimeout: 40 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get keys with prefix
	resp, err := client.Get(ctx, "", clientv3.WithPrefix())
	require.NoError(t, err)

	return resp.Kvs
}
