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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noop))
}

type noop struct {
	db        *sqlite.SQLite
	app       *app.App
	place     *placement.Placement
	scheduler *scheduler.Scheduler
}

func (n *noop) Setup(t *testing.T) []framework.Option {
	n.db = sqlite.New(t, sqlite.WithActorStateStore(true))
	n.app = app.New(t,
		app.WithConfig(`{"entities": ["myactortype"]}`),
		app.WithHandlerFunc("/actors/myactortype/myactorid", func(http.ResponseWriter, *http.Request) {}),
	)
	n.scheduler = scheduler.New(t, scheduler.WithLogLevel("debug"))
	n.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(n.db, n.scheduler, n.place, n.app),
	}
}

func (n *noop) Run(t *testing.T, ctx context.Context) {
	opts := []daprd.Option{
		daprd.WithResourceFiles(n.db.GetComponent(t)),
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithSchedulerAddresses(n.scheduler.Address()),
		daprd.WithAppPort(n.app.Port()),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: false
`),
	}

	daprd1 := daprd.New(t, opts...)
	daprd2 := daprd.New(t, opts...)

	daprd1.Run(t, ctx)
	daprd1.WaitUntilRunning(t, ctx)
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
	resp, err := n.scheduler.ETCDClient(t, ctx).KV.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
	require.NoError(t, err)
	assert.Empty(t, resp.Kvs)
	daprd1.Cleanup(t)

	daprd2.Run(t, ctx)
	daprd2.WaitUntilRunning(t, ctx)
	resp, err = n.scheduler.ETCDClient(t, ctx).KV.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
	require.NoError(t, err)
	assert.Empty(t, resp.Kvs)
	daprd2.Cleanup(t)
}
