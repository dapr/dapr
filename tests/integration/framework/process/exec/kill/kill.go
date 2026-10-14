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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/iowriter"
)

func Interrupt(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	if cmd == nil || cmd.ProcessState != nil {
		return
	}

	iowriter.Eventf(t, "interrupting %s", filepath.Base(cmd.Path))

	interrupt(t, cmd)
}

func Kill(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	if cmd == nil || cmd.ProcessState != nil {
		return
	}

	iowriter.Eventf(t, "killing %s", filepath.Base(cmd.Path))

	kill(t, cmd)
}

func SignalHUP(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	require.NotNil(t, cmd, "cmd must not be nil when sending SIGHUP")
	require.Nil(t, cmd.ProcessState, "process must still be running when sending SIGHUP")

	iowriter.Eventf(t, "signaling HUP to %s", filepath.Base(cmd.Path))

	signalHUP(t, cmd)
}
