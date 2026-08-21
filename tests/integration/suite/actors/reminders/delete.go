/*
Copyright 2025 The Dapr Authors
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

package reminders

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(delete))
}

type delete struct {
	place *delete
	sched *delete

	actors *actors.Actors
}

func (d *delete) setup(t *testing.T, extra ...actors.Option) []process.Interface {
	d.actors = actors.New(t, append([]actors.Option{
		actors.WithActorTypes("foo", "bar"),
	}, extra...)...)

	return []process.Interface{d.actors}
}

func (d *delete) Setup(t *testing.T) []framework.Option {
	d.place, d.sched = new(delete), new(delete)
	procs := d.place.setup(t)
	procs = append(procs, d.sched.setup(t, actors.WithSchedulerPlacement())...)

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (d *delete) Run(t *testing.T, ctx context.Context) {
	t.Run("placement", func(t *testing.T) { d.place.run(t, ctx) })
	t.Run("scheduler", func(t *testing.T) { d.sched.run(t, ctx) })
}

func (d *delete) run(t *testing.T, ctx context.Context) {
	d.actors.WaitUntilRunning(t, ctx)

	client := d.actors.Daprd().GRPCClient(t, ctx)

	resp, err := client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "foo", ActorId: nil,
	})
	require.NoError(t, err)
	assert.Empty(t, resp.GetReminders())

	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "foo",
		ActorId:   "abc-1",
		Name:      "reminder1",
		DueTime:   "1000s",
	})
	require.NoError(t, err)

	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "foo", ActorId: nil,
	})
	require.NoError(t, err)
	assert.Len(t, resp.GetReminders(), 1)

	_, err = client.UnregisterActorReminder(ctx, &rtv1.UnregisterActorReminderRequest{
		ActorType: "foo",
		ActorId:   "abc-1",
		Name:      "reminder1",
	})
	require.NoError(t, err)

	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "foo", ActorId: nil,
	})
	require.NoError(t, err)
	assert.Empty(t, resp.GetReminders())

	_, err = client.UnregisterActorReminder(ctx, &rtv1.UnregisterActorReminderRequest{
		ActorType: "foo",
		ActorId:   "abc-1",
		Name:      "reminder1",
	})
	require.NoError(t, err)

	for i := range 5 {
		_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "foo",
			ActorId:   "abc-" + strconv.Itoa(i),
			Name:      "reminder1",
			DueTime:   "1000s",
		})
		require.NoError(t, err)
	}
	for i := range 5 {
		_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "foo",
			ActorId:   "helloworld",
			Name:      "reminder" + strconv.Itoa(i),
			DueTime:   "1000s",
		})
		require.NoError(t, err)
	}
	for i := range 5 {
		_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "bar",
			ActorId:   "abc-" + strconv.Itoa(i),
			Name:      "reminder1",
			DueTime:   "1000s",
		})
		require.NoError(t, err)
	}

	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "foo", ActorId: nil,
	})
	require.NoError(t, err)
	assert.Len(t, resp.GetReminders(), 10)
	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "bar", ActorId: nil,
	})
	require.NoError(t, err)
	assert.Len(t, resp.GetReminders(), 5)

	_, err = client.UnregisterActorRemindersByType(ctx, &rtv1.UnregisterActorRemindersByTypeRequest{
		ActorType: "foo",
		ActorId:   new("helloworld"),
	})
	require.NoError(t, err)
	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "foo", ActorId: nil,
	})
	require.NoError(t, err)
	assert.Len(t, resp.GetReminders(), 5)

	_, err = client.UnregisterActorRemindersByType(ctx, &rtv1.UnregisterActorRemindersByTypeRequest{
		ActorType: "foo",
		ActorId:   nil,
	})
	require.NoError(t, err)
	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "foo", ActorId: nil,
	})
	require.NoError(t, err)
	assert.Empty(t, resp.GetReminders())
	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "bar", ActorId: nil,
	})
	require.NoError(t, err)
	assert.Len(t, resp.GetReminders(), 5)
}
