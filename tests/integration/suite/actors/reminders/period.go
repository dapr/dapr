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

package reminders

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(period))
}

type period struct {
	actors *actors.Actors
	count  atomic.Int64
}

func (p *period) Setup(t *testing.T) []framework.Option {
	p.count.Store(0)
	p.actors = actors.New(t,
		actors.WithActorTypes("foo"),
		actors.WithActorTypeHandler("foo", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				return
			}
			p.count.Add(1)
		}),
	)
	actors2 := actors.New(t,
		actors.WithActorTypes("foo"),
		actors.WithActorTypeHandler("foo", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				return
			}
			p.count.Add(1)
		}),
		actors.WithPeerActor(p.actors),
	)

	return []framework.Option{
		framework.WithProcesses(actors2, p.actors),
	}
}

func (p *period) Run(t *testing.T, ctx context.Context) {
	p.actors.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	url := p.actors.Daprd().ActorReminderURL("foo", "1234", "helloworld")
	body := `{"data":"reminderdata","dueTime":"1s","period":"R5/PT1S"}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(5), p.count.Load())
	}, 20*time.Second, 1*time.Second)

	time.Sleep(time.Second * 3)
	assert.Equal(t, int64(5), p.count.Load())
}
