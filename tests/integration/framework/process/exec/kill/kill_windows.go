//go:build windows
// +build windows

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

package kill

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func interrupt(t *testing.T, cmd *exec.Cmd) {
	dll, err := windows.LoadDLL("kernel32.dll")
	require.NoError(t, err)
	defer dll.Release()

	f, err := dll.FindProc("AttachConsole")
	require.NoError(t, err)

	r1, _, err := f.Call(uintptr(cmd.Process.Pid))
	if r1 == 0 && err != syscall.ERROR_ACCESS_DENIED {
		require.NoError(t, err)
	}

	f, err = dll.FindProc("SetConsoleCtrlHandler")
	require.NoError(t, err)

	r1, _, err = f.Call(0, 1)
	require.ErrorContains(t, err, "The operation completed successfully")

	f, err = dll.FindProc("GenerateConsoleCtrlEvent")
	require.NoError(t, err)

	r1, _, err = f.Call(windows.CTRL_BREAK_EVENT, uintptr(cmd.Process.Pid))
	require.ErrorContains(t, err, "The operation completed successfully")

	r1, _, err = f.Call(windows.CTRL_C_EVENT, uintptr(cmd.Process.Pid))
	require.ErrorContains(t, err, "The operation completed successfully")
}
