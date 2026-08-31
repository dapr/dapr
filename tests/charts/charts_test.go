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

package charts

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// helmTemplate renders the dapr chart with the given values, returning the
// manifests and any rendering error.
func helmTemplate(t *testing.T, setValues ...string) (string, error) {
	t.Helper()

	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is not installed")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	chartDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "charts", "dapr")

	args := []string{"template", "dapr", chartDir}
	for _, v := range setValues {
		args = append(args, "--set="+v)
	}

	out, err := exec.Command("helm", args...).CombinedOutput()
	return string(out), err
}

func TestSchedulerPlacementEnabled(t *testing.T) {
	t.Run("placement is deployed and the scheduler flag is off by default", func(t *testing.T) {
		manifests, err := helmTemplate(t)
		require.NoError(t, err, manifests)
		require.Contains(t, manifests, "name: dapr-placement-server")
		require.Contains(t, manifests, `"--placement-enabled=false"`)
	})

	t.Run("enabling removes placement and turns the scheduler flag on", func(t *testing.T) {
		manifests, err := helmTemplate(t, "global.scheduler.placement.enabled=true")
		require.NoError(t, err, manifests)
		require.NotContains(t, manifests, "name: dapr-placement-server")
		require.Contains(t, manifests, `"--placement-enabled=true"`)
	})

	t.Run("enabling requires the scheduler", func(t *testing.T) {
		manifests, err := helmTemplate(t,
			"global.scheduler.placement.enabled=true",
			"global.scheduler.enabled=false",
		)
		require.Error(t, err)
		require.Contains(t, manifests, "no service would serve actor placement")
	})

	t.Run("enabling requires the actors building block", func(t *testing.T) {
		manifests, err := helmTemplate(t,
			"global.scheduler.placement.enabled=true",
			"global.actors.enabled=false",
		)
		require.Error(t, err)
		require.Contains(t, manifests, "the actors building block is disabled")
	})

	t.Run("enabling requires placement as the actor service", func(t *testing.T) {
		manifests, err := helmTemplate(t,
			"global.scheduler.placement.enabled=true",
			"global.actors.serviceName=scheduler",
		)
		require.Error(t, err)
		require.Contains(t, manifests, "another placement authority cannot take part in the handoff")
	})

	t.Run("enabling keeps the sidecar placement address", func(t *testing.T) {
		// Sidecars keep their placement address so they can return to a
		// redeployed placement service without restarts.
		manifests, err := helmTemplate(t, "global.scheduler.placement.enabled=true")
		require.NoError(t, err, manifests)
		require.Contains(t, manifests, "dapr-placement-server:50005")
	})
}
