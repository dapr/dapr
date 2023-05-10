/*
Copyright 2023 The Dapr Authors
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

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Integration(t *testing.T) {
	if _, ok := os.LookupEnv("DAPR_INTEGRATION_DAPRD_PATH"); !ok {
		t.Log("DAPR_INTEGRATION_DAPRD_PATH not set, building daprd binary")

		_, tfile, _, ok := runtime.Caller(0)
		require.True(t, ok)
		rootDir := filepath.Join(filepath.Dir(tfile), "../..")

		// Use a consistent temp dir for the binary so that the binary is cached on
		// subsequent runs.
		daprdPath := filepath.Join(os.TempDir(), "dapr_integration_tests/daprd")
		if runtime.GOOS == "windows" {
			daprdPath += ".exe"
		}

		t.Logf("Root dir: %q", rootDir)
		t.Logf("Building daprd binary to: %q", daprdPath)
		cmd := exec.Command("go", "build", "-tags=all_components", "-v", "-o", daprdPath, filepath.Join(rootDir, "cmd/daprd"))
		cmd.Dir = rootDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		require.NoError(t, cmd.Run())

		require.NoError(t, os.Setenv("DAPR_INTEGRATION_DAPRD_PATH", daprdPath))
	}

	RunIntegrationTests(t)
}
