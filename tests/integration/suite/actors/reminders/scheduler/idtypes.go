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
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(idtype))
}

type actordaprd struct {
	actorTypes []actortype
}

type actortype struct {
	typename string
	ids      []string
}

type idtype struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler

	actorTypesNum int
	actorIDsNum   int
	daprdsNum     int

	daprds      []*daprd.Daprd
	actorDaprds []actordaprd

	lock         sync.Mutex
	methodcalled []string
	expcalled    []string
}

func (i *idtype) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: true`), 0o600))

	i.scheduler = scheduler.New(t)
	i.place = placement.New(t)

	i.daprdsNum = 10
	i.actorTypesNum = 2
	i.actorIDsNum = 50
	i.daprds = make([]*daprd.Daprd, i.daprdsNum)
	i.actorDaprds = make([]actordaprd, i.daprdsNum)
	procs := make([]process.Interface, i.daprdsNum*2+2)
	procs[0] = i.scheduler
	procs[1] = i.place

	for x := 0; x < i.daprdsNum; x++ {
		x := x
		i.actorDaprds[x].actorTypes = make([]actortype, i.actorTypesNum)

		var appOpts []app.Option
		for y := 0; y < i.actorTypesNum; y++ {
			y := y
			typeuid, err := uuid.NewUUID()
			require.NoError(t, err)
			i.actorDaprds[x].actorTypes[y].typename = typeuid.String()
			i.actorDaprds[x].actorTypes[y].ids = make([]string, i.actorIDsNum)

			for z := 0; z < i.actorIDsNum; z++ {
				z := z
				iduid, err := uuid.NewUUID()
				require.NoError(t, err)
				i.actorDaprds[x].actorTypes[y].ids[z] = iduid.String()
				i.expcalled = append(i.expcalled, fmt.Sprintf("%d/%s/%s", x, typeuid.String(), iduid.String()))

				appOpts = append(appOpts,
					app.WithHandlerFunc(
						fmt.Sprintf("/actors/%s/%s/method/remind/", typeuid.String(), iduid.String()),
						func(http.ResponseWriter, *http.Request) {
							i.lock.Lock()
							defer i.lock.Unlock()
							i.methodcalled = append(i.methodcalled, fmt.Sprintf("%d/%s/%s", x, typeuid.String(), iduid.String()))
						}),
					app.WithHandlerFunc(
						fmt.Sprintf("/actors/%s/%s/method/foo", typeuid.String(), iduid.String()),
						func(http.ResponseWriter, *http.Request) {},
					),
				)
			}
		}

		app := app.New(t, append(appOpts,
			app.WithHandlerFunc("/dapr/config",
				func(w http.ResponseWriter, r *http.Request) {
					fmt.Fprintf(w, `{"entities": ["%s", "%s"]}`,
						i.actorDaprds[x].actorTypes[0].typename,
						i.actorDaprds[x].actorTypes[1].typename,
					)
				}),
		)...)

		i.daprds[x] = daprd.New(t,
			daprd.WithConfigs(configFile),
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithPlacementAddresses(i.place.Address()),
			daprd.WithSchedulerAddresses(i.scheduler.Address()),
			daprd.WithAppPort(app.Port()),
		)

		procs[2+x*2] = i.daprds[x]
		procs[2+x*2+1] = app
	}

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (i *idtype) Run(t *testing.T, ctx context.Context) {
	i.scheduler.WaitUntilRunning(t, ctx)
	i.place.WaitUntilRunning(t, ctx)

	for x := 0; x < i.daprdsNum; x++ {
		i.daprds[x].WaitUntilRunning(t, ctx)
	}

	client := util.HTTPClient(t)

	daprdURL := "http://" + i.daprds[0].HTTPAddress() + "/v1.0/actors/"
	for x := 0; x < i.daprdsNum; x++ {
		for y := 0; y < i.actorTypesNum; y++ {
			for z := 0; z < i.actorIDsNum; z++ {
				require.EventuallyWithT(t, func(c *assert.CollectT) {
					invoke := fmt.Sprintf("%s/%s/%s/method/foo", daprdURL, i.actorDaprds[x].actorTypes[y].typename, i.actorDaprds[x].actorTypes[y].ids[z])
					req, err := http.NewRequestWithContext(ctx, http.MethodPost, invoke, nil)
					require.NoError(t, err)
					resp, err := client.Do(req)
					//nolint:testifylint
					if assert.NoError(c, err) {
						assert.NoError(c, resp.Body.Close())
						assert.Equal(c, http.StatusOK, resp.StatusCode)
					}
				}, time.Second*10, time.Millisecond*10, "actor not ready in time")
			}
		}
	}

	for x := 0; x < i.daprdsNum; x++ {
		for y := 0; y < i.actorTypesNum; y++ {
			for z := 0; z < i.actorIDsNum; z++ {
				_, err := i.daprds[x].GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
					ActorType: i.actorDaprds[x].actorTypes[y].typename,
					ActorId:   i.actorDaprds[x].actorTypes[y].ids[z],
					Name:      "remindermethod",
					DueTime:   "1s",
					Data:      []byte("reminderdata"),
				})
				require.NoError(t, err)
			}
		}
	}

	assert.Eventually(t, func() bool {
		i.lock.Lock()
		defer i.lock.Unlock()
		return len(i.methodcalled) == i.actorIDsNum*i.actorTypesNum*i.daprdsNum
	}, time.Second*20, time.Millisecond*10)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(t, i.expcalled, i.methodcalled)
	}, time.Second*20, time.Millisecond*10)
}
