//go:build !windows

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

import "syscall"

// control is a no-op on Unix-like systems. Go's net package already sets
// SO_REUSEADDR on these platforms before bind, so in-process rebind of a
// just-closed listener works without any additional socket option.
func control(network, address string, c syscall.RawConn) error {
	return nil
}
