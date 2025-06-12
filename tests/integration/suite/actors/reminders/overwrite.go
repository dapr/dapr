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
	"testing"

	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(overwrite))
}

type overwrite struct {
	actors *actors.Actors
}

func (o *overwrite) Setup(t *testing.T) []framework.Option {
	o.actors = actors.New(t,
		actors.WithActorTypes("abc"),
	)

	return []framework.Option{
		framework.WithProcesses(o.actors),
	}
}

func (o *overwrite) Run(t *testing.T, ctx context.Context) {
	o.actors.WaitUntilRunning(t, ctx)

	client := o.actors.GRPCClient(t, ctx)
	_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "abc",
		ActorId:   "123",
		Name:      "reminder1",
		DueTime:   "24h",
	})
	require.NoError(t, err)

	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "abc",
		ActorId:   "123",
		Name:      "reminder1",
		DueTime:   "48h",
	})
	require.NoError(t, err)
}
