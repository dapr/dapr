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

package os

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// WriteFileHeld creates path with no sharing, writes data, and keeps the
// handle open until the returned release is called, as an on-access scanner
// does. Every other open of the file fails with a sharing violation while it
// is held.
func WriteFileHeld(t *testing.T, path string, data []byte) (release func()) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	require.NoError(t, err)
	handle, err := syscall.CreateFile(name, syscall.GENERIC_WRITE, 0,
		nil, syscall.CREATE_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	var written uint32
	require.NoError(t, syscall.WriteFile(handle, data, &written, nil))
	require.NoError(t, syscall.FlushFileBuffers(handle))

	// Some hosts do not refuse the open with a sharing violation; skip
	// rather than test something else.
	f, oerr := os.Open(path)
	if oerr == nil {
		f.Close()
	}
	if !errors.Is(oerr, windows.ERROR_SHARING_VIOLATION) {
		require.NoError(t, syscall.CloseHandle(handle))
		t.Skipf("host did not refuse an open of a file held with share mode 0, os.Open returned: %v", oerr)
	}

	var once sync.Once
	release = func() { once.Do(func() { require.NoError(t, syscall.CloseHandle(handle)) }) }
	t.Cleanup(release)
	return release
}
