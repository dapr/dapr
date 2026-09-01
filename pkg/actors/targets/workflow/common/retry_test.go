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
	"errors"
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

func Test_CreateReminderWithRetry(t *testing.T) {
	t.Parallel()

	req := &actorapi.CreateReminderRequest{Name: "start-es-1", ActorType: "wf", ActorID: "id"}

	tests := map[string]struct {
		err       error
		wantErr   bool
		wantCalls int
	}{
		// The scheduler surfaces its cron shutdown error ("cron is closed")
		// and etcd errors as plain errors, which cross the wire as Unknown:
		// they are as transient as Unavailable and must be retried, or a
		// workflow whose start reminder hits the shutdown window is
		// stranded PENDING.
		"unknown wire error is retried": {
			err:       status.Error(codes.Unknown, "cron is closed"),
			wantErr:   false,
			wantCalls: 3,
		},
		"unavailable is retried": {
			err:       status.Error(codes.Unavailable, "connection refused"),
			wantErr:   false,
			wantCalls: 3,
		},
		"deadline exceeded is retried": {
			err:       status.Error(codes.DeadlineExceeded, "deadline"),
			wantErr:   false,
			wantCalls: 3,
		},
		"internal is retried": {
			err:       status.Error(codes.Internal, "server error"),
			wantErr:   false,
			wantCalls: 3,
		},
		// etcd surfaces a full storage quota (NOSPACE) as ResourceExhausted;
		// it only clears with operator intervention, so the bounded helper
		// surfaces it immediately rather than masking it behind the caller's
		// deadline (the forever helper still retries it as transient etcd
		// pressure).
		"resource exhausted is permanent": {
			err:       status.Error(codes.ResourceExhausted, "quota"),
			wantErr:   true,
			wantCalls: 1,
		},
		"invalid argument is permanent": {
			err:       status.Error(codes.InvalidArgument, "bad request"),
			wantErr:   true,
			wantCalls: 1,
		},
		"failed precondition is permanent": {
			err:       status.Error(codes.FailedPrecondition, "precondition"),
			wantErr:   true,
			wantCalls: 1,
		},
		"unauthenticated is permanent": {
			err:       status.Error(codes.Unauthenticated, "no identity"),
			wantErr:   true,
			wantCalls: 1,
		},
		"local non-status error is permanent": {
			err:       errors.New("marshalling exploded"),
			wantErr:   true,
			wantCalls: 1,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			creator := &failNCreator{n: 2, err: test.err}
			err := CreateReminderWithRetry(t.Context(), creator, req)
			if test.wantErr {
				require.ErrorIs(t, err, test.err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, test.wantCalls, creator.calls)
		})
	}
}

func Test_CreateReminderWithRetryForever_ResourceExhausted(t *testing.T) {
	t.Parallel()

	req := &actorapi.CreateReminderRequest{Name: "start-es-1", ActorType: "wf", ActorID: "id"}
	creator := &failNCreator{n: 2, err: status.Error(codes.ResourceExhausted, "quota")}
	err := CreateReminderWithRetryForever(t.Context(), creator, req)
	require.Error(t, err)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	assert.Equal(t, 1, creator.calls)
}
