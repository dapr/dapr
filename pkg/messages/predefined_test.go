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

package messages

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcCodes "google.golang.org/grpc/codes"
)

func TestErrActorStateStoreNotFound(t *testing.T) {
	err := ErrActorStateStoreNotFound.WithFormat("TransactionActor", "wallet-transactions")

	assert.Equal(t, "ERR_ACTOR_STATE_STORE_NOT_FOUND", err.Tag())
	assert.Equal(t, http.StatusInternalServerError, err.HTTPCode())
	assert.Equal(t, grpcCodes.Internal, err.GRPCStatus().Code())
	assert.Contains(t, err.Message(), "TransactionActor")
	assert.Contains(t, err.Message(), "wallet-transactions")
	assert.NotContains(t, err.Message(), "actorStateStore")

	require.ErrorIs(t, err, ErrActorStateStoreNotFound)
	require.NotErrorIs(t, err, ErrActorRuntimeNotFound)
}
