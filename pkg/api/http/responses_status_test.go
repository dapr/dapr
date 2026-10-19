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
	"context"
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
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	kitErrors "github.com/dapr/kit/errors"
)

func TestRespondWithComponentStatusError(t *testing.T) {
	t.Parallel()

	t.Run("a component status keeps its canonical code and details", func(t *testing.T) {
		t.Parallel()

		st, err := status.New(codes.FailedPrecondition, "cursor expired").
			WithDetails(&errdetails.ErrorInfo{Reason: "SEARCH_CONTINUATION_EXPIRED", Domain: "dapr.io"})
		require.NoError(t, err)

		rec := httptest.NewRecorder()
		require.True(t, respondWithComponentStatusError(rec, st.Err()))

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
			codes.Unavailable:      http.StatusServiceUnavailable,
			codes.Unimplemented:    http.StatusNotImplemented,
			codes.Aborted:          http.StatusConflict,
		} {
			rec := httptest.NewRecorder()
			require.True(t, respondWithComponentStatusError(rec, status.Error(code, "x")))
			assert.Equal(t, want, rec.Code, code.String())
		}
	})

	t.Run("Dapr errors and plain errors are left to respondWithError", func(t *testing.T) {
		t.Parallel()

		kitErr := kitErrors.NewBuilder(codes.NotFound, http.StatusNotFound, "no store", "ERR_TEST", "test").
			WithErrorInfo("ERR_TEST", nil).
			Build()
		for _, err := range []error{kitErr, errors.New("boom")} {
			rec := httptest.NewRecorder()
			assert.False(t, respondWithComponentStatusError(rec, err))
			assert.Zero(t, rec.Body.Len())
		}
	})

	t.Run("respondWithError keeps a plain status generic", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		respondWithError(rec, status.Error(codes.NotFound, "x"))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), errorcodes.CommonGeneric.Code)
	})
}

func TestUniversalHTTPHandlerComponentStatusErrors(t *testing.T) {
	t.Parallel()

	handler := func(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
		return nil, status.Error(codes.NotFound, "no such index")
	}

	for enabled, want := range map[bool]int{
		true:  http.StatusNotFound,
		false: http.StatusInternalServerError,
	} {
		h := UniversalHTTPHandler(handler, UniversalHTTPHandlerOpts[*emptypb.Empty, *emptypb.Empty]{
			ComponentStatusErrors: enabled,
		})
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		assert.Equal(t, want, rec.Code, "ComponentStatusErrors=%t", enabled)
	}
}
