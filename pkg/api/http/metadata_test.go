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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/api/universal"
	"github.com/dapr/dapr/pkg/resiliency"
)

// TestOnPutMetadataBodyReadError verifies that a body-read failure on
// PUT /v1.0/metadata/{key} returns the rich kiterrors error with
// ERR_BODY_READ code (standardized per issue #7489) rather than
// the legacy messages.ErrBodyRead plain error.
func TestOnPutMetadataBodyReadError(t *testing.T) {
	t.Parallel()

	testAPI := &api{
		universal: universal.New(universal.Options{
			Logger:     log,
			Resiliency: resiliency.New(nil),
		}),
	}

	// Set up a minimal chi router for the PUT /metadata/{key} route
	r := chi.NewRouter()
	r.Put("/v1.0/metadata/{key}", testAPI.onPutMetadata())

	// Use a pipe whose write end is closed with an error so io.ReadAll fails
	pr, pw := io.Pipe()
	pw.CloseWithError(errors.New("simulated read error"))

	req := httptest.NewRequest(http.MethodPut, "/v1.0/metadata/myKey", pr)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "ERR_BODY_READ", body["errorCode"])
}
