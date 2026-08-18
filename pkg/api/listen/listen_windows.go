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

package listen

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// control sets SO_REUSEADDR on the listen socket. On Windows this allows
// the same process to immediately rebind a port it just closed, which is
// what we need for SIGHUP-driven runtime restart cycles. Without it, the
// kernel can hold the bind for several seconds while accepted connections
// from the previous listener drain.
func control(network, address string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd),
			windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	}); err != nil {
		return err
	}
	return serr
}
