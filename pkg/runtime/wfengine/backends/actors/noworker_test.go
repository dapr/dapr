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

package actors

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	inmemory "github.com/dapr/components-contrib/state/in-memory"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/kit/logger"
)

func TestNoWorkerError(t *testing.T) {
	const (
		localType  = "dapr.internal.default.myapp.workflow"
		remoteType = "dapr.internal.default.otherapp.workflow"
	)

	newBackend := func(t *testing.T, withStore bool) *Actors {
		t.Helper()
		cs := compstore.New()
		if withStore {
			require.NoError(t, cs.AddStateStoreActor("mystore", inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))))
		}
		be, err := New(Options{AppID: "myapp", Namespace: "default", ComponentStore: cs})
		require.NoError(t, err)
		return be
	}

	noAddress := fmt.Errorf("failed to lookup actor: %w", messages.ErrActorNoAddress.WithFormat(localType+"/abc"))
	notRegistered := fmt.Errorf("%w: actor type %s not registered", actorerrors.ErrCreatingActor, localType)
	remoteNotRegistered := status.Error(codes.Internal, "error invoke actor method: failed to create actor: actor type "+remoteType+" not registered")
	remoteNoAddress := status.Error(codes.FailedPrecondition, "failed to lookup actor: api error: code = FailedPrecondition desc = did not find address for actor '"+remoteType+"/abc'")

	for name, test := range map[string]struct {
		err    error
		target string
		appID  string
	}{
		"local no address":         {err: noAddress, appID: "myapp"},
		"local not registered":     {err: notRegistered, appID: "myapp"},
		"remote not registered":    {err: remoteNotRegistered, target: "otherapp", appID: "otherapp"},
		"remote no address":        {err: remoteNoAddress, target: "otherapp", appID: "otherapp"},
		"target same as local app": {err: noAddress, target: "myapp", appID: "myapp"},
	} {
		t.Run(name, func(t *testing.T) {
			err := newBackend(t, true).noWorkerError(test.err, test.target)
			require.ErrorIs(t, err, wferrors.ErrNoWorkerConnected)
			require.ErrorIs(t, err, test.err)
			assert.Contains(t, err.Error(), "no workflow worker is connected for app '"+test.appID+"'")
		})
	}

	t.Run("no address keeps its error code", func(t *testing.T) {
		err := newBackend(t, true).noWorkerError(noAddress, "")
		require.ErrorIs(t, err, messages.ErrActorNoAddress)
		assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	})

	t.Run("unrelated errors pass through", func(t *testing.T) {
		be := newBackend(t, true)
		require.NoError(t, be.noWorkerError(nil, ""))
		other := errors.New("boom")
		assert.Same(t, other, be.noWorkerError(other, ""))
	})

	t.Run("another app's routing error is not attributed to the target", func(t *testing.T) {
		// A recursive purge on otherapp failing to reach a child on thirdapp.
		childErr := status.Error(codes.Internal, `error invoke actor method: failed to purge child workflow "c": failed to lookup actor: api error: code = FailedPrecondition desc = did not find address for actor 'dapr.internal.default.thirdapp.workflow/c'`)
		assert.Same(t, childErr, newBackend(t, true).noWorkerError(childErr, "otherapp"))

		other := fmt.Errorf("%w: actor type dapr.internal.default.otherapp2.workflow not registered", actorerrors.ErrCreatingActor)
		assert.Same(t, other, newBackend(t, true).noWorkerError(other, "otherapp"))
	})

	t.Run("no actor state store reports the state store", func(t *testing.T) {
		err := newBackend(t, false).noWorkerError(noAddress, "")
		require.ErrorIs(t, err, messages.ErrActorRuntimeNotFound)
	})
}
