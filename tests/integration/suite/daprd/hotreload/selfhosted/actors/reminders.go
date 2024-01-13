/*
Copyright 2023 The Dapr Authors
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

package actors

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reminders))
}

type reminders struct {
	daprd *daprd.Daprd
	place *placement.Placement

	resDir       string
	methodcalled atomic.Int64
}

func (r *reminders) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true
`), 0o600))

	r.resDir = t.TempDir()

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/remindermethod", func(http.ResponseWriter, *http.Request) {
		r.methodcalled.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {})

	require.NoError(t, os.WriteFile(filepath.Join(r.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: inmem
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: actorStateStore
    value: true
`), 0o600))

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	r.place = placement.New(t)
	r.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(r.resDir),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(r.place, srv, r.daprd),
	}
}

func (r *reminders) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	client := r.daprd.GRPCClient(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		//nolint:testifylint
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*100)

	_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
		DueTime:   "0ms",
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return r.methodcalled.Load() == 1
	}, time.Second*3, time.Millisecond*100)

	require.NoError(t, os.Remove(filepath.Join(r.resDir, "1.yaml")))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(t, ctx, util.HTTPClient(t), r.daprd.HTTPPort()), 1)
	}, time.Second*5, time.Millisecond*100)
	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
		DueTime:   "0ms",
	})
	require.ErrorContains(t, err, "actors: state store does not exist or incorrectly configured")

	require.NoError(t, os.WriteFile(filepath.Join(r.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: inmem
spec:
  type: state.in-memory
  version: v1
`), 0o600))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(t, ctx, util.HTTPClient(t), r.daprd.HTTPPort()), 2)
	}, time.Second*5, time.Millisecond*100)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(t, ctx, util.HTTPClient(t), r.daprd.HTTPPort()), 2)
	}, time.Second*5, time.Millisecond*100)
	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
		DueTime:   "0ms",
	})
	require.ErrorContains(t, err, "actors: state store does not exist or incorrectly configured")

	require.NoError(t, os.WriteFile(filepath.Join(r.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: inmem
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: actorStateStore
    value: true
`), 0o600))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Name:      "remindermethod",
			DueTime:   "0ms",
		})
		//nolint:testifylint
		assert.NoError(c, err)
	}, time.Second*5, time.Millisecond*100)

	assert.Eventually(t, func() bool {
		return r.methodcalled.Load() == 2
	}, time.Second*3, time.Millisecond*100)
}
