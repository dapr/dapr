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

package util

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// FreePort reserves a network ports, and then frees them when the test is
// ready to run.
type FreePort struct {
	ports []int
	lsns  []net.Listener
}

func ReservePorts(t *testing.T, count int) *FreePort {
	t.Helper()
	var ports []int
	var lsns []net.Listener

	for i := 0; i < count; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
		lsns = append(lsns, ln)
	}

	return &FreePort{
		ports: ports,
		lsns:  lsns,
	}
}

func (f *FreePort) Port(t *testing.T, n int) int {
	t.Helper()

	if n >= len(f.ports) {
		t.Fatalf("port index out of range: %d", n)
	}

	return f.ports[n]
}

func (f *FreePort) Free(t *testing.T) {
	t.Helper()
	for _, l := range f.lsns {
		require.NoError(t, l.Close())
	}
}
