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

package scheduler

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(staging))
}

type staging struct {
	actors1 *actors.Actors
	actors2 *actors.Actors
	got     atomic.Int64
}

func (s *staging) Setup(t *testing.T) []framework.Option {
	s.actors1 = actors.New(t,
		actors.WithActorTypes("foo"),
		actors.WithActorTypeHandler("foo", func(_ http.ResponseWriter, req *http.Request) {
			assert.Fail(t, "unexpected foo call")
		}),
	)
	s.actors2 = actors.New(t,
		actors.WithDB(s.actors1.DB()),
		actors.WithPlacement(s.actors1.Placement()),
		actors.WithScheduler(s.actors1.Scheduler()),
		actors.WithActorTypes("bar"),
		actors.WithActorTypeHandler("bar", func(_ http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodDelete {
				return
			}
			s.got.Add(1)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(s.actors1),
	}
}

func (s *staging) Run(t *testing.T, ctx context.Context) {
	s.actors1.WaitUntilRunning(t, ctx)

	_, err := s.actors1.Scheduler().Client(t, ctx).ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "helloworld",
		Job:  &schedulerv1.Job{DueTime: ptr.Of(time.Now().Format(time.RFC3339))},
		Metadata: &schedulerv1.JobMetadata{
			Namespace: "default", AppId: s.actors1.AppID(),
			Target: &schedulerv1.JobTargetMetadata{
				Type: &schedulerv1.JobTargetMetadata_Actor{
					Actor: &schedulerv1.TargetActorReminder{
						Type: "bar", Id: "1234",
					},
				},
			},
		},
	})
	require.NoError(t, err)

	time.Sleep(time.Second * 2)
	assert.Equal(t, int64(0), s.got.Load())

	t.Cleanup(func() { s.actors2.Cleanup(t) })
	s.actors2.Run(t, ctx)
	s.actors2.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), s.got.Load())
	}, time.Second*10, time.Millisecond*10)
	s.actors2.Cleanup(t)
}
