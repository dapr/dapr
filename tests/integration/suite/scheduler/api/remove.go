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

package api

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(remove))
}

type remove struct {
	scheduler *scheduler.Scheduler

	etcdPort int
}

func (r *remove) Setup(t *testing.T) []framework.Option {
	fp := ports.Reserve(t, 2)
	port1 := fp.Port(t)
	port2 := fp.Port(t)

	r.etcdPort = port2

	clientPorts := []string{
		"scheduler-0=" + strconv.Itoa(r.etcdPort),
	}
	r.scheduler = scheduler.New(t,
		scheduler.WithID("scheduler-0"),
		scheduler.WithInitialCluster(fmt.Sprintf("scheduler-0=http://localhost:%d", port1)),
		scheduler.WithInitialClusterPorts(port1),
		scheduler.WithEtcdClientPorts(clientPorts),
	)

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(fp, r.scheduler),
	}
}

func (r *remove) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)

	etcdClient := client.Etcd(t, clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("localhost:%d", r.etcdPort)},
		DialTimeout: 5 * time.Second,
	})

	client := r.scheduler.Client(t, ctx)

	watch, err := client.WatchJobs(ctx)
	require.NoError(t, err)
	require.NoError(t, watch.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Initial{
			Initial: &schedulerv1.WatchJobsRequestInitial{
				AppId:     "appid",
				Namespace: "namespace",
			},
		},
	}))

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "test",
		Job: &schedulerv1.Job{
			Schedule: ptr.Of("@every 20s"),
			DueTime:  ptr.Of(time.Now().Format(time.RFC3339)),
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

	// Use "path/filepath" import, it is using OS specific path separator unlike "path"
	etcdKeysPrefix := filepath.Join("dapr", "jobs")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys, rerr := etcdClient.ListAllKeys(ctx, etcdKeysPrefix)
		require.NoError(c, rerr)
		assert.Len(c, keys, 1)
	}, time.Second*10, 10*time.Millisecond)

	job, err := watch.Recv()
	require.NoError(t, err)
	require.NoError(t, watch.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
			Result: &schedulerv1.WatchJobsRequestResult{
				Id: job.GetId(),
			},
		},
	}))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys, rerr := etcdClient.ListAllKeys(ctx, etcdKeysPrefix)
		require.NoError(c, rerr)
		assert.Len(c, keys, 1)
	}, time.Second*10, 10*time.Millisecond)

	_, err = client.DeleteJob(ctx, &schedulerv1.DeleteJobRequest{
		Name: "test",
		Metadata: &schedulerv1.JobMetadata{
			AppId:     "appid",
			Namespace: "namespace",
			Target: &schedulerv1.JobTargetMetadata{
				Type: new(schedulerv1.JobTargetMetadata_Job),
			},
		},
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys, rerr := etcdClient.ListAllKeys(ctx, etcdKeysPrefix)
		require.NoError(c, rerr)
		assert.Empty(c, keys)
	}, time.Second*10, 10*time.Millisecond)
}
