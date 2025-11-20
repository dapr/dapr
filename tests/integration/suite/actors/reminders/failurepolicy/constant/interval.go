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

package constant

import (
	"context"
	"net/http"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	corev1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(interval))
}

type interval struct {
	actors    *actors.Actors
	triggered chan string
}

func (i *interval) Setup(t *testing.T) []framework.Option {
	i.triggered = make(chan string, 10)

	i.actors = actors.New(t,
		actors.WithActorTypes("foo"),
		actors.WithActorTypeHandler("foo", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				i.triggered <- path.Base(r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}),
	)

	return []framework.Option{
		framework.WithProcesses(i.actors),
	}
}

func (i *interval) Run(t *testing.T, ctx context.Context) {
	i.actors.WaitUntilRunning(t, ctx)

	_, err := i.actors.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "foo",
		ActorId:   "1234",
		Name:      "test",
		DueTime:   "0s",
		FailurePolicy: &corev1.JobFailurePolicy{
			Policy: &corev1.JobFailurePolicy_Constant{
				Constant: &corev1.JobFailurePolicyConstant{
					Interval:   durationpb.New(time.Second * 3),
					MaxRetries: nil,
				},
			},
		},
	})
	require.NoError(t, err)

	// Should trigger once immediately
	select {
	case name := <-i.triggered:
		assert.Equal(t, "test", name)
	case <-time.After(time.Second * 1):
		require.Fail(t, "timed out waiting for job")
	}

	// Should not trigger again for 2 seconds
	select {
	case <-i.triggered:
		assert.Fail(t, "unexpected trigger")
	case <-time.After(time.Second * 2):
	}

	// Should trigger again after 3 seconds since first trigger
	select {
	case name := <-i.triggered:
		assert.Equal(t, "test", name)
	case <-time.After(time.Second * 5):
		require.Fail(t, "timed out waiting for job")
	}
}
