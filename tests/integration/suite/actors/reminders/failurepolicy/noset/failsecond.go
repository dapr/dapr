/*
Copyright 2024 The Dapr Authors
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

package noset

import (
	"context"
	"net/http"
	"path"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(failsecond))
}

type failsecond struct {
	actors    *actors.Actors
	triggered slice.Slice[string]
	respErr   atomic.Bool
}

func (f *failsecond) Setup(t *testing.T) []framework.Option {
	f.triggered = slice.String()
	f.respErr.Store(true)

	scheduler := scheduler.New(t)
	f.actors = actors.New(t,
		actors.WithScheduler(scheduler),
		actors.WithFeatureSchedulerReminders(true),
		actors.WithActorTypes("helloworld"),
		actors.WithActorTypeHandler("helloworld", func(w http.ResponseWriter, req *http.Request) {
			defer f.triggered.Append(path.Base(req.URL.Path))
			if f.respErr.Load() {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}),
	)

	return []framework.Option{
		framework.WithProcesses(scheduler, f.actors),
	}
}

func (f *failsecond) Run(t *testing.T, ctx context.Context) {
	f.actors.WaitUntilRunning(t, ctx)

	_, err := f.actors.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "helloworld",
		ActorId:   "1234",
		Name:      "test",
		DueTime:   "0s",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"test", "test"}, f.triggered.Slice())
	}, time.Second*10, time.Millisecond*10)

	f.respErr.Store(false)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"test", "test", "test"}, f.triggered.Slice())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.ElementsMatch(t, []string{"test", "test", "test"}, f.triggered.Slice())
}
