/*
Copyright 2026 The Dapr Authors
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

package selfhosted

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(actorstateresolved))
}

// actorstateresolved ensures that, while an actor state store is loaded, hot
// reload decides whether another state store is marked as the actor state
// store the same way the state processor does: on the resolved metadata, with
// the last of duplicate entries winning. A store that the processor would mark
// is skipped rather than failing its init and exiting daprd, and a store whose
// environment variable resolves to false is installed.
type actorstateresolved struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
	resDir  string
}

func (a *actorstateresolved) Setup(t *testing.T) []framework.Option {
	place := placement.New(t)

	a.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Skipping hot reload of dupstore",
		),
	)

	a.resDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "state.yaml"), []byte(actorStateStoreComp), 0o600))

	a.daprd = daprd.New(t,
		daprd.WithResourcesDir(a.resDir),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithExecOptions(
			exec.WithStdout(a.logline.Stdout()),
			exec.WithEnvVars(t, "ACTORSTATERESOLVED_MARKED", "false"),
		),
	)

	return []framework.Option{
		framework.WithProcesses(place, a.logline, a.daprd),
	}
}

func (a *actorstateresolved) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "dupstore.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: dupstore
spec:
 type: state.in-memory
 version: v1
 metadata:
 - name: actorStateStore
   value: "false"
 - name: actorStateStore
   value: "true"
`), 0o600))

	assert.Eventually(t, a.logline.FoundAll, time.Second*20, time.Millisecond*10)

	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "envstore.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: envstore
spec:
 type: state.in-memory
 version: v1
 metadata:
 - name: actorStateStore
   envRef: ACTORSTATERESOLVED_MARKED
`), 0o600))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		comps := a.daprd.GetMetaRegisteredComponents(c, ctx)
		names := make([]string, 0, len(comps))
		for _, comp := range comps {
			names = append(names, comp.GetName())
		}
		assert.Contains(c, names, "envstore")
		assert.NotContains(c, names, "dupstore")
	}, time.Second*20, time.Millisecond*10)

	// daprd is still serving, with the original actor state store.
	gclient := a.daprd.GRPCClient(t, ctx)
	resp, err := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Equal(t, rtv1.ActorRuntime_RUNNING, resp.GetActorRuntime().GetRuntimeStatus())
}
