//go:build windows

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
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/kit/signals"
)

func interrupt(_ *testing.T, cmd *exec.Cmd) {
	kill(nil, cmd)
}

func kill(_ *testing.T, cmd *exec.Cmd) {
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	kill.Stdout = os.Stdout
	kill.Stderr = os.Stderr
	kill.Run()
}

func signalHUP(t *testing.T, cmd *exec.Cmd) {
	require.NoError(t, signals.SignalReload(cmd.Process.Pid))
}
