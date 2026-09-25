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

package errors

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	kiterrors "github.com/dapr/kit/errors"
)

func TestStateStoreGet(t *testing.T) {
	err := StateStore("mystore").Get("mykey", "connection refused")
	require.Error(t, err)

	kerr, ok := kiterrors.FromError(err)
	require.True(t, ok)

	require.Equal(t, http.StatusInternalServerError, kerr.HTTPStatusCode())
	require.Equal(t, codes.Internal, kerr.GrpcStatusCode())
	require.Equal(t, errorcodes.StateGet.Code, kerr.ErrorCode())
	require.Contains(t, kerr.Error(), "failed to get mykey from state store mystore: connection refused")
}
