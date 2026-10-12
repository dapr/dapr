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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
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

		// os.Open does not ask for FILE_SHARE_DELETE, so removing the file
		// underneath it fails with a sharing violation instead of leaving it
		// delete pending. Only a handle which shares delete, as a watcher's
		// does, reproduces the state under test.
		name, err := syscall.UTF16PtrFromString(path)
		require.NoError(t, err)
		handle, err := syscall.CreateFile(name, syscall.GENERIC_READ,
			syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
			nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
		require.NoError(t, err)
		t.Cleanup(func() { _ = syscall.CloseHandle(handle) })

		require.NoError(t, os.Remove(path))

		data, err := ReadDirs([]string{dir})
		require.NoError(t, err, "a delete pending file must not fail the scan")
		require.Len(t, data.Entries, 1)
		assert.Empty(t, data.Entries[0].Files)
	})
}

func TestIsLockedWindows(t *testing.T) {
	t.Run("should recognise a sharing violation", func(t *testing.T) {
		assert.True(t, isLocked(windows.ERROR_SHARING_VIOLATION))
		assert.False(t, isLocked(syscall.ERROR_ACCESS_DENIED))
	})

	// lock opens path without sharing, as an on-access scanner does, and
	// returns the handle for the test to close.
	lock := func(t *testing.T, path string) syscall.Handle {
		t.Helper()
		name, err := syscall.UTF16PtrFromString(path)
		require.NoError(t, err)
		handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0,
			nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
		require.NoError(t, err)
		return handle
	}

	t.Run("should read a file once a brief exclusive hold is released", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "1.yaml")
		require.NoError(t, os.WriteFile(path, []byte("a"), 0o600))
		handle := lock(t, path)
		released := make(chan struct{})
		go func() {
			defer close(released)
			time.Sleep(readRetryDelay * 2)
			_ = syscall.CloseHandle(handle)
		}()

		data, err := ReadDirs([]string{dir})
		<-released
		require.NoError(t, err)
		require.Len(t, data.Entries, 1)
		require.Len(t, data.Entries[0].Files, 1)
		assert.Equal(t, []byte("a"), data.Entries[0].Files[0].Content)
	})

	t.Run("should report a file still held exclusively after the retries", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "1.yaml")
		require.NoError(t, os.WriteFile(path, []byte("a"), 0o600))
		handle := lock(t, path)
		t.Cleanup(func() { _ = syscall.CloseHandle(handle) })

		data, err := ReadDirs([]string{dir})
		require.ErrorIs(t, err, ErrLocked, "a locked file must abort the scan so it is retried, not skipped")
		assert.Nil(t, data)
	})
}
