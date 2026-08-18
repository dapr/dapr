//go:build windows

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

package reconciler

import (
	"os"

	"github.com/dapr/kit/signals"
)

// sendSIGHUP triggers a reload of the current process on Windows by connecting
// to the named pipe that the main loop is listening on, which is the Windows
// equivalent of sending SIGHUP on POSIX systems.
func sendSIGHUP() error {
	return signals.SignalReload(os.Getpid())
}
