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

package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
)

func TestRespondWithErrorStatus(t *testing.T) {
	t.Parallel()

	t.Run("a component status keeps its canonical code and details", func(t *testing.T) {
		t.Parallel()

		st, err := status.New(codes.FailedPrecondition, "cursor expired").
			WithDetails(&errdetails.ErrorInfo{Reason: "SEARCH_CONTINUATION_EXPIRED", Domain: "dapr.io"})
		require.NoError(t, err)

		rec := httptest.NewRecorder()
		respondWithError(rec, st.Err())

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		var body struct {
			ErrorCode string            `json:"errorCode"`
			Message   string            `json:"message"`
			Details   []json.RawMessage `json:"details"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "FAILED_PRECONDITION", body.ErrorCode)
		assert.Equal(t, "cursor expired", body.Message)
		require.Len(t, body.Details, 1)
		assert.Contains(t, string(body.Details[0]), "SEARCH_CONTINUATION_EXPIRED")
		assert.Contains(t, string(body.Details[0]), "google.rpc.ErrorInfo")
	})

	t.Run("status codes map to their HTTP equivalents", func(t *testing.T) {
		t.Parallel()

		for code, want := range map[codes.Code]int{
			codes.AlreadyExists:    http.StatusConflict,
			codes.NotFound:         http.StatusNotFound,
			codes.InvalidArgument:  http.StatusBadRequest,
			codes.DeadlineExceeded: http.StatusGatewayTimeout,
			codes.Unimplemented:    http.StatusNotImplemented,
			codes.Aborted:          http.StatusConflict,
		} {
			rec := httptest.NewRecorder()
			respondWithError(rec, status.Error(code, "x"))
			assert.Equal(t, want, rec.Code, code.String())
		}
	})

	t.Run("a plain error is still generic", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		respondWithError(rec, errors.New("boom"))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), errorcodes.CommonGeneric.Code)
	})
}
