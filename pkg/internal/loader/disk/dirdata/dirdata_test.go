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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadDirs(t *testing.T) {
	t.Run("should read yaml files", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "1.yaml"), []byte("a"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "2.yml"), []byte("b"), 0o600))

		data, err := ReadDirs([]string{dir})
		require.NoError(t, err)
		require.Len(t, data.Entries, 1)
		assert.ElementsMatch(t, []FileEntry{
			{Name: "1.yaml", Content: []byte("a")},
			{Name: "2.yml", Content: []byte("b")},
		}, data.Entries[0].Files)
	})

	t.Run("should ignore files which are not yaml", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("a"), 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "nested"), 0o750))

		data, err := ReadDirs([]string{dir})
		require.NoError(t, err)
		require.Len(t, data.Entries, 1)
		assert.Empty(t, data.Entries[0].Files)
	})

	t.Run("should error when a directory is missing", func(t *testing.T) {
		_, err := ReadDirs([]string{filepath.Join(t.TempDir(), "nope")})
		require.Error(t, err)
	})

	t.Run("should skip a file deleted between listing and read", func(t *testing.T) {
		// The race this stands in for is a resource file removed while the
		// hot reload watcher is scanning. Reading a name that is no longer
		// there must not fail the whole scan, since the watcher event which
		// follows re-runs it.
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "1.yaml"), []byte("a"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "2.yaml"), []byte("b"), 0o600))
		require.NoError(t, os.Remove(filepath.Join(dir, "2.yaml")))

		data, err := ReadDirs([]string{dir})
		require.NoError(t, err)
		require.Len(t, data.Entries, 1)
		assert.Equal(t, []FileEntry{{Name: "1.yaml", Content: []byte("a")}}, data.Entries[0].Files)
	})
}

func TestIsDeletePending(t *testing.T) {
	t.Run("should not treat a missing file as delete pending", func(t *testing.T) {
		_, err := os.ReadFile(filepath.Join(t.TempDir(), "nope"))
		require.Error(t, err)
		assert.False(t, isDeletePending(err))
	})

	t.Run("should not treat a nil error as delete pending", func(t *testing.T) {
		assert.False(t, isDeletePending(nil))
	})
}
