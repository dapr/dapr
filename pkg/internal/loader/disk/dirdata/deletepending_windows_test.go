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

package dirdata

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsDeletePendingWindows(t *testing.T) {
	t.Run("should recognise access denied", func(t *testing.T) {
		assert.True(t, isDeletePending(syscall.ERROR_ACCESS_DENIED))
	})

	t.Run("should skip a file deleted while another handle holds it open", func(t *testing.T) {
		// Removing a file that is still open leaves it delete pending: it
		// stays in the directory listing, but opening it fails with access
		// denied until the last handle closes. This is the state a resource
		// file is in when the hot reload watcher still has it open, and the
		// scan has to tolerate it rather than fail the whole reload.
		dir := t.TempDir()
		path := filepath.Join(dir, "1.yaml")
		require.NoError(t, os.WriteFile(path, []byte("a"), 0o600))

		f, err := os.Open(path)
		require.NoError(t, err)
		t.Cleanup(func() { f.Close() })

		require.NoError(t, os.Remove(path))

		data, err := ReadDirs([]string{dir})
		require.NoError(t, err, "a delete pending file must not fail the scan")
		require.Len(t, data.Entries, 1)
		assert.Empty(t, data.Entries[0].Files)
	})
}
