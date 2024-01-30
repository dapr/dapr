/*
Copyright 2024 The Dapr Authors
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
	suite.Register(new(actorstate))
}

type actorstate struct {
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

func (a *actorstate) Setup(t *testing.T) []framework.Option {
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

	a.loglineCreate = logline.New(t, logline.WithStdoutLineContains(
		"Aborting to hot-reload a state store component that is used as an actor state store: mystore (state.in-memory/v1)",
	))
	a.loglineUpdate = logline.New(t, logline.WithStdoutLineContains(
		"Aborting to hot-reload a state store component that is used as an actor state store: mystore (pubsub.in-memory/v1)",
	))
	a.loglineDelete = logline.New(t, logline.WithStdoutLineContains(
		"Aborting to hot-reload a state store component that is used as an actor state store: mystore (state.in-memory/v1)",
	))

	a.resDirCreate = t.TempDir()
	a.resDirUpdate = t.TempDir()
	a.resDirDelete = t.TempDir()

	place := placement.New(t)

	a.daprdCreate = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(a.resDirCreate),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithExecOptions(
			exec.WithStdout(a.loglineCreate.Stdout()),
		),
	)

	a.daprdUpdate = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(a.resDirUpdate),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithExecOptions(
			exec.WithStdout(a.loglineUpdate.Stdout()),
		),
	)

	require.NoError(t, os.WriteFile(filepath.Join(a.resDirUpdate, "state.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mystore
spec:
 type: state.in-memory
 version: v1
 metadata:
 - name: actorstatestore
   value: "true"
`), 0o600))

	a.daprdDelete = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(a.resDirDelete),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithExecOptions(
			exec.WithStdout(a.loglineDelete.Stdout()),
		),
	)

	require.NoError(t, os.WriteFile(filepath.Join(a.resDirDelete, "state.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mystore
spec:
 type: state.in-memory
 version: v1
 metadata:
 - name: actorstatestore
   value: "true"
`), 0o600))

	return []framework.Option{
		framework.WithProcesses(place,
			a.loglineCreate, a.loglineUpdate, a.loglineDelete,
			a.daprdCreate, a.daprdUpdate, a.daprdDelete,
		),
	}
}

func (a *actorstate) Run(t *testing.T, ctx context.Context) {
	a.daprdCreate.WaitUntilRunning(t, ctx)
	a.daprdUpdate.WaitUntilRunning(t, ctx)
	a.daprdDelete.WaitUntilRunning(t, ctx)

	httpClient := util.HTTPClient(t)

	require.Empty(t, util.GetMetaComponents(t, ctx, httpClient, a.daprdCreate.HTTPPort()))
	require.NoError(t, os.WriteFile(filepath.Join(a.resDirCreate, "state.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mystore
spec:
 type: state.in-memory
 version: v1
 metadata:
 - name: actorstatestore
   value: "true"
`), 0o600))
	a.loglineCreate.EventuallyFoundAll(t)

	comps := util.GetMetaComponents(t, ctx, httpClient, a.daprdUpdate.HTTPPort())
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{
			Name: "mystore", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
	}, comps)

	require.NoError(t, os.WriteFile(filepath.Join(a.resDirUpdate, "state.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mystore
spec:
 type: pubsub.in-memory
 version: v1
 metadata:
 - name: actorstatestore
   value: "true"
`), 0o600))
	a.loglineUpdate.EventuallyFoundAll(t)

	comps = util.GetMetaComponents(t, ctx, httpClient, a.daprdDelete.HTTPPort())
	require.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{
			Name: "mystore", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
	}, comps)

	require.NoError(t, os.Remove(filepath.Join(a.resDirDelete, "state.yaml")))
	a.loglineDelete.EventuallyFoundAll(t)
}
