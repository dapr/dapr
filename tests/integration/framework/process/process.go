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

package process

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// Interface is an interface for running and cleaning up a process.
type Interface interface {
	Run(*testing.T, context.Context)
	Cleanup(*testing.T)
}

func FreePort(t *testing.T) int {
	t.Helper()
	n, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer n.Close()
	return n.Addr().(*net.TCPAddr).Port
}
