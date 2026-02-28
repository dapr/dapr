/*
Copyright 2025 The Dapr Authors
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

package os

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func SkipWindows(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on windows which relies on unix process signals")
	}
}

func WriteFileYaml(t *testing.T, data string) string {
	t.Helper()
	return writeFile(t, "yaml", data)
}

func WriteFileTo(t *testing.T, name, data string) {
	t.Helper()
	require.NoError(t, os.WriteFile(name, []byte(data), 0o600))
}

func writeFile(t *testing.T, fileType, data string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "test."+fileType)
	require.NoError(t, os.WriteFile(f, []byte(data), 0o600))
	return f
}
