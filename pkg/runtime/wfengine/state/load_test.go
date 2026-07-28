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

package state

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/actors/api"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/ptr"
)

func TestLoadWorkflowState_ConcurrentSaveMismatch(t *testing.T) {
	t.Parallel()

	const actorID = "wf-load-race"

	marshalMeta := func(t *testing.T, meta *backend.BackendWorkflowStateMetadata) []byte {
		t.Helper()
		b, err := proto.Marshal(meta)
		require.NoError(t, err)
		return b
	}

	type attempt struct {
		meta *backend.BackendWorkflowStateMetadata
		etag *string
		bulk map[string]api.BulkStateEntry
	}

	newStore := func(t *testing.T, attempts []attempt, gets *int) *statefake.Fake {
		t.Helper()
		return statefake.New().
			WithGetFn(func(_ context.Context, req *api.GetStateRequest, _ bool) (*api.StateResponse, error) {
				require.Equal(t, MetadataKey, req.Key)
				require.Less(t, *gets, len(attempts), "more load attempts than scripted")
				a := attempts[*gets]
				*gets++
				return &api.StateResponse{Data: marshalMeta(t, a.meta), ETag: a.etag}, nil
			}).
			WithGetBulkFn(func(_ context.Context, req *api.GetBulkStateRequest, _ bool) (api.BulkStateResponse, error) {
				a := attempts[*gets-1]
				out := api.BulkStateResponse{}
				for _, k := range req.Keys {
					out[k] = a.bulk[k]
				}
				return out, nil
			})
	}

	histBytes, err := proto.Marshal(testEvent(0))
	require.NoError(t, err)
	inboxBytes, err := proto.Marshal(testEvent(1))
	require.NoError(t, err)

	t.Run("inbox mismatch with changed etag retries and succeeds", func(t *testing.T) {
		t.Parallel()
		var gets int
		store := newStore(t, []attempt{
			{
				meta: &backend.BackendWorkflowStateMetadata{InboxLength: 1, Generation: 1},
				etag: ptr.Of("e1"),
				bulk: map[string]api.BulkStateEntry{},
			},
			{
				meta: &backend.BackendWorkflowStateMetadata{HistoryLength: 1, Generation: 1},
				etag: ptr.Of("e2"),
				bulk: map[string]api.BulkStateEntry{"history-000000": {Data: histBytes}},
			},
		}, &gets)

		got, lerr := LoadWorkflowState(t.Context(), store, actorID, testOpts())
		require.NoError(t, lerr)
		require.NotNil(t, got)
		assert.Empty(t, got.Inbox)
		assert.Len(t, got.History, 1)
		assert.Equal(t, 2, gets)
	})

	t.Run("history mismatch with changed etag retries and succeeds", func(t *testing.T) {
		t.Parallel()
		var gets int
		store := newStore(t, []attempt{
			{
				meta: &backend.BackendWorkflowStateMetadata{HistoryLength: 2, Generation: 1},
				etag: ptr.Of("e1"),
				bulk: map[string]api.BulkStateEntry{"history-000000": {Data: histBytes}},
			},
			{
				meta: &backend.BackendWorkflowStateMetadata{HistoryLength: 1, InboxLength: 1, Generation: 1},
				etag: ptr.Of("e2"),
				bulk: map[string]api.BulkStateEntry{
					"history-000000": {Data: histBytes},
					"inbox-000000":   {Data: inboxBytes},
				},
			},
		}, &gets)

		got, lerr := LoadWorkflowState(t.Context(), store, actorID, testOpts())
		require.NoError(t, lerr)
		require.NotNil(t, got)
		assert.Len(t, got.Inbox, 1)
		assert.Len(t, got.History, 1)
		assert.Equal(t, 2, gets)
	})

	t.Run("unchanged etag is a hard error without further retries", func(t *testing.T) {
		t.Parallel()
		var gets int
		missing := attempt{
			meta: &backend.BackendWorkflowStateMetadata{InboxLength: 1, Generation: 1},
			etag: ptr.Of("e1"),
			bulk: map[string]api.BulkStateEntry{},
		}
		store := newStore(t, []attempt{missing, missing}, &gets)

		got, lerr := LoadWorkflowState(t.Context(), store, actorID, testOpts())
		require.ErrorContains(t, lerr, "declared in metadata")
		assert.Nil(t, got)
		assert.Equal(t, 2, gets)
	})

	t.Run("changing etags retry until the attempt cap", func(t *testing.T) {
		t.Parallel()
		var gets int
		attempts := make([]attempt, loadStateMaxAttempts)
		for i := range attempts {
			attempts[i] = attempt{
				meta: &backend.BackendWorkflowStateMetadata{InboxLength: 1, Generation: 1},
				etag: ptr.Of(strconv.Itoa(i)),
				bulk: map[string]api.BulkStateEntry{},
			}
		}
		store := newStore(t, attempts, &gets)

		got, lerr := LoadWorkflowState(t.Context(), store, actorID, testOpts())
		require.ErrorContains(t, lerr, "declared in metadata")
		assert.Nil(t, got)
		assert.Equal(t, loadStateMaxAttempts, gets)
	})

	t.Run("nil etags retry until the attempt cap", func(t *testing.T) {
		t.Parallel()
		var gets int
		missing := attempt{
			meta: &backend.BackendWorkflowStateMetadata{InboxLength: 1, Generation: 1},
			bulk: map[string]api.BulkStateEntry{},
		}
		attempts := make([]attempt, loadStateMaxAttempts)
		for i := range attempts {
			attempts[i] = missing
		}
		store := newStore(t, attempts, &gets)

		got, lerr := LoadWorkflowState(t.Context(), store, actorID, testOpts())
		require.ErrorContains(t, lerr, "declared in metadata")
		assert.Nil(t, got)
		assert.Equal(t, loadStateMaxAttempts, gets)
	})

	t.Run("signing certificate verification error does not retry", func(t *testing.T) {
		t.Parallel()
		var gets int
		store := newStore(t, []attempt{{
			meta: &backend.BackendWorkflowStateMetadata{HistoryLength: 1, SigningCertificateLength: 1, Generation: 1},
			etag: ptr.Of("e1"),
			bulk: map[string]api.BulkStateEntry{"history-000000": {Data: histBytes}},
		}}, &gets)

		_, lerr := LoadWorkflowState(t.Context(), store, actorID, testOpts())
		var verifyErr *wferrors.VerificationError
		require.ErrorAs(t, lerr, &verifyErr)
		assert.Equal(t, 1, gets)
	})
}
