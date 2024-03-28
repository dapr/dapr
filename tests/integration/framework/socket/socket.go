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

package socket

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/dapr/dapr/tests/integration/framework/util"
)

// Socket is a helper to create a temporary directory hosting unix socket files
// for Dapr pluggable components.
type Socket struct {
	dir string
}

type File struct {
	name     string
	filename string
}

func New(t *testing.T) *Socket {
	if runtime.GOOS == "windows" {
		t.Skip("Unix sockets are not supported on Windows")
	}

	// Darwin enforces a maximum 104 byte socket name limit, so we need to be a
	// bit fancy on how we generate the name.
	tmp, err := nettest.LocalPath()
	require.NoError(t, err)

	socketDir := filepath.Join(tmp, util.RandomString(t, 4))
	require.NoError(t, os.MkdirAll(socketDir, 0o700))
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(tmp)) })

	return &Socket{
		dir: socketDir,
	}
}

func (s *Socket) Directory() string {
	return s.dir
}

func (s *Socket) File(t *testing.T) *File {
	socketFile := util.RandomString(t, 8)
	return &File{
		name:     socketFile,
		filename: filepath.Join(s.dir, socketFile+".sock"),
	}
}

func (f *File) Name() string {
	return f.name
}

func (f *File) Filename() string {
	return f.filename
}
