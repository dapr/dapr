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

package common

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
)

// failNCreator fails the first n Create calls with err, then succeeds.
type failNCreator struct {
	n     int
	err   error
	calls int
}

func (f *failNCreator) Create(ctx context.Context, req *actorapi.CreateReminderRequest) error {
	f.calls++
	if f.calls <= f.n {
		return f.err
	}
	return nil
}

// A create without overwrite answers AlreadyExists when a retry follows a
// create whose success was lost in transit: the reminder is there, so the
// helper reports success instead of failing the create.
func Test_CreateReminderWithRetry_AlreadyExistsIsSuccess(t *testing.T) {
	t.Parallel()

	req := &actorapi.CreateReminderRequest{Name: "activity-result-abc", ActorType: "wf", ActorID: "id"}
	creator := &failNCreator{n: 10, err: status.Error(codes.AlreadyExists, "job already exists")}
	require.NoError(t, CreateReminderWithRetry(t.Context(), creator, req))
	assert.Equal(t, 1, creator.calls)
}

func Test_CreateReminderWithRetry_PermanentErrorIsReturned(t *testing.T) {
	t.Parallel()

	req := &actorapi.CreateReminderRequest{Name: "activity-result-abc", ActorType: "wf", ActorID: "id"}
	creator := &failNCreator{n: 10, err: status.Error(codes.PermissionDenied, "denied")}
	require.Equal(t, codes.PermissionDenied, status.Code(CreateReminderWithRetry(t.Context(), creator, req)))
	assert.Equal(t, 1, creator.calls)
}
