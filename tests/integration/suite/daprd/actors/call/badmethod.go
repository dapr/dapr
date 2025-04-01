/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package call

import (
	"context"
	"fmt"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(badmethod))
}

type badmethod struct {
	actors *actors.Actors
}

func (b *badmethod) Setup(t *testing.T) []framework.Option {
	b.actors = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/ii", func(w nethttp.ResponseWriter, r *nethttp.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(b.actors),
	}
}

func (b *badmethod) Run(t *testing.T, ctx context.Context) {
	b.actors.WaitUntilRunning(t, ctx)

	url := fmt.Sprintf("http://%s/v1.0/actors/abc/ii/method/foo", b.actors.Daprd().HTTPAddress())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, nethttp.StatusInternalServerError, resp.StatusCode)
}
