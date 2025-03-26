/*
Copyright 2025 The Dapr Authors
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

package period

import (
	"context"
	"net/http"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(fail))
}

type fail struct {
	actors    *actors.Actors
	triggered slice.Slice[string]
}

func (f *fail) Setup(t *testing.T) []framework.Option {
	f.triggered = slice.String()

	f.actors = actors.New(t,
		actors.WithActorTypes("helloworld"),
		actors.WithActorTypeHandler("helloworld", func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodDelete {
				return
			}
			f.triggered.Append(path.Base(req.URL.Path))
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(f.actors),
	}
}

func (f *fail) Run(t *testing.T, ctx context.Context) {
	f.actors.WaitUntilRunning(t, ctx)

	_, err := f.actors.GRPCClient(t, ctx).RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "helloworld",
		ActorId:   "1234",
		Name:      "test",
		DueTime:   "0s",
		Period:    "R3/PT1S",
	})
	require.NoError(t, err)

	exp := []string{"test", "test", "test"}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, exp, f.triggered.Slice())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.ElementsMatch(t, exp, f.triggered.Slice())
}
