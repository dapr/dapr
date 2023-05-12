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
	"time"
)

func Kill(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	if cmd == nil || cmd.ProcessState != nil {
		return
	}

	t.Log("interrupting daprd process")

	interrupt(t, cmd)

	if filepath.Base(cmd.Path) == "daprd" {
		// TODO: daprd does not currently gracefully exit on a single interrupt
		// signal. Remove once fixed.
		time.Sleep(time.Millisecond * 300)
		interrupt(t, cmd)
	}
}
