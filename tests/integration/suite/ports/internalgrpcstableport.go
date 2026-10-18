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

package ports

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/ports"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(internalgrpcstableport))
}

// internalgrpcstableport verifies that daprd starts when its API gRPC port is
// the port the internal gRPC port would stably hash to. The internal listener
// is reserved first, so without exclusions it takes that port and the API
// server fails to bind.
type internalgrpcstableport struct {
	daprd *procdaprd.Daprd
}

func (i *internalgrpcstableport) Setup(t *testing.T) []framework.Option {
	const appID = "internalgrpcstableport"

	// daprd inherits this process' working directory, UID and hostname, so it
	// derives the same stable port for the same app ID.
	stable, err := ports.GetStablePort(47300, appID)
	require.NoError(t, err)

	i.daprd = procdaprd.New(t,
		procdaprd.WithAppID(appID),
		procdaprd.WithGRPCPort(stable),
		procdaprd.WithoutInternalGRPCPort(),
	)

	return []framework.Option{
		framework.WithProcesses(i.daprd),
	}
}

func (i *internalgrpcstableport) Run(t *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(t, ctx)
}
