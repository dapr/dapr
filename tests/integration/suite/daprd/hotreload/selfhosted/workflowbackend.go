/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package selfhosted

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(workflowbackend))
}

type workflowbackend struct {
	daprdCreate *daprd.Daprd
	daprdUpdate *daprd.Daprd
	daprdDelete *daprd.Daprd

	resDirCreate string
	resDirUpdate string
	resDirDelete string

	loglineCreate *logline.LogLine
	loglineUpdate *logline.LogLine
	loglineDelete *logline.LogLine
}

func (w *workflowbackend) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true`), 0o600))

	w.loglineCreate = logline.New(t, logline.WithStdoutLineContains(
		"Aborting to hot-reload a workflowbackend component which is not supported: wfbackend (workflowbackend.actors/v1)",
	))
	w.loglineUpdate = logline.New(t, logline.WithStdoutLineContains(
		"Aborting to hot-reload a workflowbackend component which is not supported: wfbackend (pubsub.in-memory/v1)",
	))
	w.loglineDelete = logline.New(t, logline.WithStdoutLineContains(
		"Aborting to hot-reload a workflowbackend component which is not supported: wfbackend (workflowbackend.actors/v1)",
	))

	w.resDirCreate = t.TempDir()
	w.resDirUpdate = t.TempDir()
	w.resDirDelete = t.TempDir()

	place := placement.New(t)

	w.daprdCreate = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(w.resDirCreate),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithExecOptions(
			exec.WithStdout(w.loglineCreate.Stdout()),
		),
	)

	w.daprdUpdate = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(w.resDirUpdate),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithExecOptions(
			exec.WithStdout(w.loglineUpdate.Stdout()),
		),
	)

	require.NoError(t, os.WriteFile(filepath.Join(w.resDirUpdate, "wf.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: wfbackend
spec:
 type: workflowbackend.actors
 version: v1
`), 0o600))

	w.daprdDelete = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(w.resDirDelete),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithExecOptions(
			exec.WithStdout(w.loglineDelete.Stdout()),
		),
	)

	require.NoError(t, os.WriteFile(filepath.Join(w.resDirDelete, "wf.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: wfbackend
spec:
 type: workflowbackend.actors
 version: v1
`), 0o600))

	return []framework.Option{
		framework.WithProcesses(place,
			w.loglineCreate, w.loglineUpdate, w.loglineDelete,
			w.daprdCreate, w.daprdUpdate, w.daprdDelete,
		),
	}
}

func (w *workflowbackend) Run(t *testing.T, ctx context.Context) {
	w.daprdCreate.WaitUntilRunning(t, ctx)
	w.daprdUpdate.WaitUntilRunning(t, ctx)
	w.daprdDelete.WaitUntilRunning(t, ctx)

	httpClient := util.HTTPClient(t)

	comps := util.GetMetaComponents(t, ctx, httpClient, w.daprdCreate.HTTPPort())
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{
			Name: "mystore", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
	}, comps)
	require.NoError(t, os.WriteFile(filepath.Join(w.resDirCreate, "wf.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: wfbackend
spec:
 type: workflowbackend.actors
 version: v1
`), 0o600))
	w.loglineCreate.EventuallyFoundAll(t)

	comps = util.GetMetaComponents(t, ctx, httpClient, w.daprdUpdate.HTTPPort())
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{Name: "wfbackend", Type: "workflowbackend.actors", Version: "v1"},
		{
			Name: "mystore", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
	}, comps)

	require.NoError(t, os.WriteFile(filepath.Join(w.resDirUpdate, "wf.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: wfbackend
spec:
 type: pubsub.in-memory
 version: v1
`), 0o600))
	w.loglineUpdate.EventuallyFoundAll(t)

	comps = util.GetMetaComponents(t, ctx, httpClient, w.daprdDelete.HTTPPort())
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{Name: "wfbackend", Type: "workflowbackend.actors", Version: "v1"},
		{
			Name: "mystore", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
	}, comps)

	require.NoError(t, os.Remove(filepath.Join(w.resDirDelete, "wf.yaml")))
	w.loglineDelete.EventuallyFoundAll(t)
}
