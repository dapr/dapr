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

package constant

import (
	"context"
	"net/http"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(maxretries))
}

type maxretries struct {
	actors    *actors.Actors
	triggered slice.Slice[string]
}

func (m *maxretries) Setup(t *testing.T) []framework.Option {
	m.triggered = slice.String()

	m.actors = actors.New(t,
		actors.WithActorTypes("foo"),
		actors.WithActorTypeHandler("foo", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				m.triggered.Append(path.Base(r.URL.Path))
				w.WriteHeader(http.StatusInternalServerError)
			}
		}),
	)

	return []framework.Option{
		framework.WithProcesses(m.actors),
	}
}

func (m *maxretries) Run(t *testing.T, ctx context.Context) {
	m.actors.WaitUntilRunning(t, ctx)

	_, err := m.actors.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "foo",
		ActorId:   "1234",
		Name:      "test",
		DueTime:   "0s",
		FailurePolicy: &corev1.JobFailurePolicy{
			Policy: &corev1.JobFailurePolicy_Constant{
				Constant: &corev1.JobFailurePolicyConstant{
					Interval:   nil,
					MaxRetries: ptr.Of(uint32(4)),
				},
			},
		},
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"test", "test", "test", "test", "test"}, m.triggered.Slice())
	}, time.Second*10, time.Millisecond*10)

	time.Sleep(time.Second * 2)
	assert.ElementsMatch(t, []string{"test", "test", "test", "test", "test"}, m.triggered.Slice())
}
