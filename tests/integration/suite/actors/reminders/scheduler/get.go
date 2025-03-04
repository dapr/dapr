/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or impliei.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scheduler

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(get))
}

type get struct {
	actors *actors.Actors
}

func (g *get) Setup(t *testing.T) []framework.Option {
	g.actors = actors.New(t,
		actors.WithActorTypes("foo"),
		actors.WithActorTypeHandler("foo", func(http.ResponseWriter, *http.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(g.actors),
	}
}

func (g *get) Run(t *testing.T, ctx context.Context) {
	g.actors.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	url := g.actors.Daprd().ActorReminderURL("foo", "1234", "helloworld")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	// Not found returns 200.
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	body := `{"data":"reminderdata","dueTime":"1s","period":"1s"}`
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, `{"period":"@every 1s","data":"reminderdata","actorID":"1234","actorType":"foo","dueTime":"1s"}`, strings.TrimSpace(string(b)))
}
