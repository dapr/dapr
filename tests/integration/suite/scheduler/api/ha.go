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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(ha))
}

type ha struct {
	scheduler1 *scheduler.Scheduler
	scheduler2 *scheduler.Scheduler
	scheduler3 *scheduler.Scheduler

	clientPorts []int
	etcdClient  *clientv3.Client
	idPrefix    string
}

func (h *ha) Setup(t *testing.T) []framework.Option {
	uuid, err := uuid.NewUUID()
	require.NoError(t, err)
	h.idPrefix = uuid.String()

	fp := ports.Reserve(t, 6)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)

	opts := []scheduler.Option{
		scheduler.WithInitialCluster(fmt.Sprintf("scheduler-0=http://localhost:%d,scheduler-1=http://localhost:%d,scheduler-2=http://localhost:%d", port1, port2, port3)),
		scheduler.WithInitialClusterPorts(port1, port2, port3),
		scheduler.WithReplicaCount(3),
	}

	h.clientPorts = []int{fp.Port(t), fp.Port(t), fp.Port(t)}

	clientPorts := []string{
		"scheduler-0=" + strconv.Itoa(h.clientPorts[0]),
		"scheduler-1=" + strconv.Itoa(h.clientPorts[1]),
		"scheduler-2=" + strconv.Itoa(h.clientPorts[2]),
	}

	h.scheduler1 = scheduler.New(t, append(opts, scheduler.WithID("scheduler-0"), scheduler.WithEtcdClientPorts(clientPorts))...)
	h.scheduler2 = scheduler.New(t, append(opts, scheduler.WithID("scheduler-1"), scheduler.WithEtcdClientPorts(clientPorts))...)
	h.scheduler3 = scheduler.New(t, append(opts, scheduler.WithID("scheduler-2"), scheduler.WithEtcdClientPorts(clientPorts))...)

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(fp, h.scheduler1, h.scheduler2, h.scheduler3),
	}
}

func (h *ha) Run(t *testing.T, ctx context.Context) {
	h.scheduler1.WaitUntilRunning(t, ctx)
	h.scheduler2.WaitUntilRunning(t, ctx)
	h.scheduler3.WaitUntilRunning(t, ctx)

	var err error
	h.etcdClient, err = clientv3.New(clientv3.Config{
		Endpoints: []string{
			fmt.Sprintf("127.0.0.1:%d", h.clientPorts[0]),
			fmt.Sprintf("127.0.0.1:%d", h.clientPorts[1]),
			fmt.Sprintf("127.0.0.1:%d", h.clientPorts[2]),
		},
		DialTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, h.etcdClient.Close())
	})

	client := h.scheduler1.Client(t, ctx)

	t.Run("CRUD 10 jobs", func(t *testing.T) {
		for i := 1; i <= 10; i++ {
			name := h.idPrefix + "_" + strconv.Itoa(i)

			req := &schedulerv1.ScheduleJobRequest{
				Name: name,
				Job: &schedulerv1.Job{
					Schedule: ptr.Of("@every 20s"),
					Repeats:  ptr.Of(uint32(1)),
					Data: &anypb.Any{
						Value: []byte(h.idPrefix),
					},
					Ttl: ptr.Of("30s"),
				},
				Metadata: &schedulerv1.JobMetadata{
					AppId:     "appid",
					Namespace: "namespace",
					Target: &schedulerv1.JobTargetMetadata{
						Type: new(schedulerv1.JobTargetMetadata_Job),
					},
				},
			}

			_, err := client.ScheduleJob(ctx, req)
			require.NoError(t, err)

			assert.True(t, h.etcdHasJob(t, ctx, name))

			resp, err := client.GetJob(ctx, &schedulerv1.GetJobRequest{
				Name: name,
				Metadata: &schedulerv1.JobMetadata{
					AppId:     "appid",
					Namespace: "namespace",
					Target: &schedulerv1.JobTargetMetadata{
						Type: new(schedulerv1.JobTargetMetadata_Job),
					},
				},
			})
			require.NoError(t, err)
			assert.Equal(t, "@every 20s", resp.GetJob().GetSchedule())
			assert.Equal(t, uint32(1), resp.GetJob().GetRepeats())
			assert.Equal(t, "30s", resp.GetJob().GetTtl())
			assert.Equal(t, &anypb.Any{
				Value: []byte(h.idPrefix),
			}, resp.GetJob().GetData())
		}

		for i := 1; i <= 10; i++ {
			name := h.idPrefix + "_" + strconv.Itoa(i)

			_, err := client.DeleteJob(ctx, &schedulerv1.DeleteJobRequest{
				Name: name,
				Metadata: &schedulerv1.JobMetadata{
					AppId:     "appid",
					Namespace: "namespace",
					Target: &schedulerv1.JobTargetMetadata{
						Type: new(schedulerv1.JobTargetMetadata_Job),
					},
				},
			})
			require.NoError(t, err)

			assert.False(t, h.etcdHasJob(t, ctx, name))
		}
	})
}

func (h *ha) etcdHasJob(t *testing.T, ctx context.Context, key string) bool {
	t.Helper()

	// Get keys with prefix
	resp, err := h.etcdClient.Get(ctx, "", clientv3.WithPrefix())
	require.NoError(t, err)

	for _, kv := range resp.Kvs {
		if strings.HasSuffix(string(kv.Key), "||"+key) {
			return true
		}
	}

	return false
}
