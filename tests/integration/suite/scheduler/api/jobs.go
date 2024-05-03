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
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(jobs))
}

// schedulejobs tests daprd scheduling jobs against the scheduler.
type jobs struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler

	clientPort int
	idPrefix   string
}

func (j *jobs) Setup(t *testing.T) []framework.Option {
	j.idPrefix = uuid.NewString()

	fp := ports.Reserve(t, 2)
	port1 := fp.Port(t)
	port2 := fp.Port(t)

	j.clientPort = port2

	clientPorts := []string{
		"scheduler-0=" + strconv.Itoa(j.clientPort),
	}
	j.scheduler = scheduler.New(t,
		scheduler.WithID("scheduler-0"),
		scheduler.WithInitialCluster(fmt.Sprintf("scheduler-0=http://localhost:%d", port1)),
		scheduler.WithInitialClusterPorts(port1),
		scheduler.WithEtcdClientPorts(clientPorts),
	)

	j.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(j.scheduler.Address()),
	)

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(fp, j.daprd, j.scheduler),
	}
}

func (j *jobs) Run(t *testing.T, ctx context.Context) {
	j.scheduler.WaitUntilRunning(t, ctx)
	j.daprd.WaitUntilRunning(t, ctx)

	client := j.daprd.GRPCClient(t, ctx)

	t.Run("CRUD 10 jobs", func(t *testing.T) {
		for i := 1; i <= 10; i++ {
			name := j.idPrefix + "_" + strconv.Itoa(i)

			req := &rtv1.ScheduleJobRequest{
				Job: &rtv1.Job{
					Name:     name,
					Schedule: ptr.Of("@every 1s"),
					Repeats:  ptr.Of(uint32(1)),
					Data: &anypb.Any{
						Value: []byte(j.idPrefix),
					},
					Ttl: ptr.Of("20s"),
				},
			}

			_, err := client.ScheduleJob(ctx, req)
			require.NoError(t, err)

			chosenSchedulerEtcdKeys := getEtcdKeys(t, ctx, j.clientPort)
			assert.True(t, checkKeysForAppID(name, chosenSchedulerEtcdKeys))

			resp, err := client.GetJob(ctx, &rtv1.GetJobRequest{Name: name})
			require.NotNil(t, resp)
			require.Equal(t, name, resp.GetJob().GetName())
			require.NoError(t, err)
		}

		for i := 1; i <= 10; i++ {
			name := j.idPrefix + "_" + strconv.Itoa(i)

			_, err := client.DeleteJob(ctx, &rtv1.DeleteJobRequest{Name: name})
			require.NoError(t, err)

			chosenSchedulerEtcdKeys := getEtcdKeys(t, ctx, j.clientPort)
			assert.False(t, checkKeysForAppID(name, chosenSchedulerEtcdKeys))
		}
	})
}

func getEtcdKeys(t *testing.T, ctx context.Context, port int) []*mvccpb.KeyValue {
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("localhost:%d", port)},
		DialTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	defer client.Close()

	// Get keys with prefix
	resp, err := client.Get(ctx, "", clientv3.WithPrefix())
	require.NoError(t, err)

	return resp.Kvs
}

func checkKeysForAppID(key string, keys []*mvccpb.KeyValue) bool {
	for _, kv := range keys {
		if strings.HasSuffix(string(kv.Key), "||"+key) {
			return true
		}
	}

	return false
}
