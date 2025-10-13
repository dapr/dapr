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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(skip))
}

type skip struct {
	db        *sqlite.SQLite
	app       *app.App
	place     *placement.Placement
	scheduler *scheduler.Scheduler

	skiplog *logline.LogLine
}

func (s *skip) Setup(t *testing.T) []framework.Option {
	s.db = sqlite.New(t, sqlite.WithActorStateStore(true))
	s.app = app.New(t,
		app.WithConfig(`{"entities": ["myactortype"]}`),
		app.WithHandlerFunc("/actors/myactortype/myactorid", func(http.ResponseWriter, *http.Request) {}),
	)
	s.scheduler = scheduler.New(t)
	s.place = placement.New(t)

	s.skiplog = logline.New(t, logline.WithStdoutLineContains(
		"Skipping migration of reminders to scheduler as requested.",
	))

	return []framework.Option{
		framework.WithProcesses(s.skiplog, s.db, s.scheduler, s.place, s.app),
	}
}

func (s *skip) Run(t *testing.T, ctx context.Context) {
	opts := []daprd.Option{
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.scheduler.Address()),
		daprd.WithAppPort(s.app.Port()),
	}

	daprd1 := daprd.New(t, append(opts,
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: false
`))...)

	daprd2 := daprd.New(t,
		append(opts,
			daprd.WithLogLineStdout(s.skiplog),
			daprd.WithSkipStateStoreReminderMigration(t),
		)...,
	)

	daprd1.Run(t, ctx)
	daprd1.WaitUntilRunning(t, ctx)

	assert.Empty(t, s.scheduler.EtcdJobs(t, ctx))
	assert.Empty(t, s.db.ActorReminders(t, ctx, "myactortype").Reminders)

	client := daprd1.GRPCClient(t, ctx)
	_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "myreminder",
		DueTime:   "10000s",
		Period:    "10000s",
		Data:      []byte("mydata"),
		Ttl:       "10000s",
	})
	require.NoError(t, err)
	assert.Len(t, s.db.ActorReminders(t, ctx, "myactortype").Reminders, 1)
	assert.Empty(t, s.scheduler.EtcdJobs(t, ctx))
	daprd1.Cleanup(t)

	daprd2.Run(t, ctx)
	daprd2.WaitUntilRunning(t, ctx)
	assert.Len(t, s.db.ActorReminders(t, ctx, "myactortype").Reminders, 1)
	time.Sleep(time.Second * 3)
	assert.Empty(t, s.scheduler.EtcdJobs(t, ctx), 0)
	daprd2.Cleanup(t)
}
