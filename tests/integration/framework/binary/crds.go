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

package binary

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework/iowriter"
)

// generateCRDs runs the controllergen helper once at build time and stores the
// output for tests to consume.
func generateCRDs(t *testing.T) {
	t.Helper()

	const name = "generated_crds"
	if _, ok := os.LookupEnv(EnvKey(name)); ok {
		t.Logf("%q set, using %q pre-generated CRDs", EnvKey(name), EnvValue(name))
		return
	}

	outPath := filepath.Join(tmpDir(), "dapr_integration_tests", "generated_crds.yaml")
	t.Logf("%q not set, generating CRDs to %q", EnvKey(name), outPath)

	ioerr := iowriter.New(t, "controllergen")
	var stdout bytes.Buffer
	//nolint:gosec
	cmd := exec.Command(EnvValue("controllergen"),
		"crd:crdVersions=v1",
		"paths=github.com/dapr/dapr/pkg/apis/...",
		"output:stdout",
	)
	cmd.Dir = RootDir(t)
	cmd.Stdout = &stdout
	cmd.Stderr = ioerr
	assert.NoError(t, cmd.Run())
	assert.NoError(t, ioerr.Close())

	assert.NoError(t, os.MkdirAll(filepath.Dir(outPath), 0o700))
	assert.NoError(t, os.WriteFile(outPath, stdout.Bytes(), 0o600))
	//nolint:usetesting
	assert.NoError(t, os.Setenv(EnvKey(name), outPath))
}
