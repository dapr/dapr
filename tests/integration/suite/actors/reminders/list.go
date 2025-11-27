/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or impliei.
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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(list))
}

type list struct {
	actors *actors.Actors
}

func (l *list) Setup(t *testing.T) []framework.Option {
	l.actors = actors.New(t,
		actors.WithActorTypes("foo", "bar"),
	)

	return []framework.Option{
		framework.WithProcesses(l.actors),
	}
}

func (l *list) Run(t *testing.T, ctx context.Context) {
	l.actors.WaitUntilRunning(t, ctx)

	client := l.actors.Daprd().GRPCClient(t, ctx)

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

	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "bar", ActorId: nil,
	})
	require.NoError(t, err)
	assert.Empty(t, resp.GetReminders())

	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "foo", ActorId: ptr.Of("abc-"),
	})
	require.NoError(t, err)
	assert.Empty(t, resp.GetReminders())

	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "foo", ActorId: ptr.Of("abc-1"),
	})
	require.NoError(t, err)
	assert.Len(t, resp.GetReminders(), 1)

	for i := 2; i <= 5; i++ {
		_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "foo",
			ActorId:   "abc-1",
			Name:      "reminder" + strconv.Itoa(i),
			DueTime:   "1000s",
		})
		require.NoError(t, err)
	}

	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "foo", ActorId: ptr.Of("abc-1"),
	})
	require.NoError(t, err)
	assert.Len(t, resp.GetReminders(), 5)

	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "foo",
		ActorId:   "abc-2",
		Name:      "reminder1",
		DueTime:   "1000s",
	})
	require.NoError(t, err)
	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "foo", ActorId: ptr.Of("abc-1"),
	})
	require.NoError(t, err)
	assert.Len(t, resp.GetReminders(), 5)

	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "foo", ActorId: nil,
	})
	require.NoError(t, err)
	assert.Len(t, resp.GetReminders(), 6)

	resp, err = client.ListActorReminders(ctx, &rtv1.ListActorRemindersRequest{
		ActorType: "foo", ActorId: ptr.Of("abc-2"),
	})
	require.NoError(t, err)
	assert.Len(t, resp.GetReminders(), 1)
}
