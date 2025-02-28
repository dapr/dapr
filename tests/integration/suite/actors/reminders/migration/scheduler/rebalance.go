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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(rebalance))
}

type rebalance struct {
	actor1 *actors.Actors
	actor2 *actors.Actors
}

func (r *rebalance) Setup(t *testing.T) []framework.Option {
	r.actor1 = actors.New(t,
		actors.WithActorTypes("myactortype"),
		actors.WithFeatureSchedulerReminders(false),
	)
	r.actor2 = actors.New(t,
		actors.WithDB(r.actor1.DB()),
		actors.WithScheduler(r.actor1.Scheduler()),
		actors.WithActorTypes("myactortype"),
		actors.WithPlacement(r.actor1.Placement()),
		actors.WithFeatureSchedulerReminders(true),
	)

	return []framework.Option{
		framework.WithProcesses(r.actor1, r.actor2),
	}
}

func (r *rebalance) Run(t *testing.T, ctx context.Context) {
	r.actor1.WaitUntilRunning(t, ctx)
	r.actor2.WaitUntilRunning(t, ctx)

	assert.Empty(t, r.actor1.DB().ActorReminders(t, ctx, "myactortype").Reminders)
	assert.Empty(t, r.actor2.DB().ActorReminders(t, ctx, "myactortype").Reminders)
	assert.Empty(t, r.actor1.Scheduler().EtcdJobs(t, ctx))
	assert.Empty(t, r.actor2.Scheduler().EtcdJobs(t, ctx))

	for i := range 200 {
		_, err := r.actor1.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "initial-" + strconv.Itoa(i),
			Name:      "foo",
			DueTime:   "1000s",
		})
		require.NoError(t, err)
	}

	assert.Len(t, r.actor1.DB().ActorReminders(t, ctx, "myactortype").Reminders, 200)
	assert.Len(t, r.actor2.DB().ActorReminders(t, ctx, "myactortype").Reminders, 200)

	assert.Empty(t, r.actor1.Scheduler().EtcdJobs(t, ctx))

	t.Run("new daprd", func(t *testing.T) {
		daprd := actors.New(t,
			actors.WithDB(r.actor1.DB()),
			actors.WithActorTypes("myactortype"),
			actors.WithFeatureSchedulerReminders(true),
			actors.WithPlacement(r.actor1.Placement()),
			actors.WithScheduler(r.actor1.Scheduler()),
		)
		t.Cleanup(func() { daprd.Cleanup(t) })
		daprd.Run(t, ctx)
		daprd.WaitUntilRunning(t, ctx)

		assert.Len(t, r.actor1.DB().ActorReminders(t, ctx, "myactortype").Reminders, 200)
		assert.Len(t, r.actor2.DB().ActorReminders(t, ctx, "myactortype").Reminders, 200)
		assert.Len(t, daprd.DB().ActorReminders(t, ctx, "myactortype").Reminders, 200)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.NotEmpty(c, r.actor1.Scheduler().EtcdJobs(t, ctx))
			assert.NotEmpty(c, daprd.Scheduler().EtcdJobs(t, ctx))
		}, time.Second*5, time.Millisecond*10)
		daprd.Cleanup(t)
	})
}
