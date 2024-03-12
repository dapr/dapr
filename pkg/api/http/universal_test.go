/*
Copyright 2022 The Dapr Authors
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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/messages"
)

func TestUniversalHTTPHandler(t *testing.T) {
	pingPongHandler := func(ctx context.Context, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
		msg := in.GetValue()
		switch msg {
		case "ping":
			return wrapperspb.String("pong"), nil
		case "modified":
			return wrapperspb.String("🧐"), nil
		case "empty":
			return nil, nil
		default:
			return nil, messages.ErrBadRequest.WithFormat("unexpected message")
		}
	}

	t.Run("Base scenario without body", func(t *testing.T) {
		// Create the handler
		h := UniversalHTTPHandler(
			func(ctx context.Context, in *emptypb.Empty) (*wrapperspb.StringValue, error) {
				return wrapperspb.String("👋"), nil
			},
			UniversalHTTPHandlerOpts[*emptypb.Empty, *wrapperspb.StringValue]{},
		)
		require.NotNil(t, h)

		// Create the request
		r := httptest.NewRequest(http.MethodGet, "http://localhost/test", nil)

		// Execute the handler
		w := httptest.NewRecorder()
		h(w, r)

		// Assertions
		resp := w.Result()
		defer resp.Body.Close()
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("content-type"))

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, `"👋"`, string(respBody))
	})

	t.Run("Base scenario with body", func(t *testing.T) {
		// Create the handler
		h := UniversalHTTPHandler(
			pingPongHandler,
			UniversalHTTPHandlerOpts[*wrapperspb.StringValue, *wrapperspb.StringValue]{},
		)
		require.NotNil(t, h)

		// Create the request
		r := httptest.NewRequest(http.MethodPost, "http://localhost/test", strings.NewReader(`"ping"`))
		r.Header.Set("content-type", "application/json")

		// Execute the handler
		w := httptest.NewRecorder()
		h(w, r)

		// Assertions
		resp := w.Result()
		defer resp.Body.Close()
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("content-type"))

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, `"pong"`, string(respBody))
	})

	t.Run("Body is invalid", func(t *testing.T) {
		// Create the handler
		h := UniversalHTTPHandler(
			pingPongHandler,
			UniversalHTTPHandlerOpts[*wrapperspb.StringValue, *wrapperspb.StringValue]{},
		)
		require.NotNil(t, h)

		// Create the request
		r := httptest.NewRequest(http.MethodPost, "http://localhost/test", strings.NewReader(`not-some-json`))
		r.Header.Set("content-type", "application/json")

		// Execute the handler
		w := httptest.NewRecorder()
		h(w, r)

		// Assertions
		resp := w.Result()
		defer resp.Body.Close()
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("content-type"))

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Contains(t, string(respBody), `"ERR_MALFORMED_REQUEST"`)
	})

	t.Run("Handler returns error", func(t *testing.T) {
		// Create the handler
		h := UniversalHTTPHandler(
			pingPongHandler,
			UniversalHTTPHandlerOpts[*wrapperspb.StringValue, *wrapperspb.StringValue]{},
		)
		require.NotNil(t, h)

		// Create the request
		r := httptest.NewRequest(http.MethodPost, "http://localhost/test", strings.NewReader(`"fail"`))
		r.Header.Set("content-type", "application/json")

		// Execute the handler
		w := httptest.NewRecorder()
		h(w, r)

		// Assertions
		resp := w.Result()
		defer resp.Body.Close()
		require.NotNil(t, resp)
		assert.Equal(t, messages.ErrBadRequest.HTTPCode(), resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("content-type"))

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, `{"errorCode":"ERR_BAD_REQUEST","message":"invalid request: unexpected message"}`, string(respBody))
	})

	t.Run("Handler returns nil", func(t *testing.T) {
		// Create the handler
		h := UniversalHTTPHandler(
			pingPongHandler,
			UniversalHTTPHandlerOpts[*wrapperspb.StringValue, *wrapperspb.StringValue]{},
		)
		require.NotNil(t, h)

		// Create the request
		r := httptest.NewRequest(http.MethodPost, "http://localhost/test", strings.NewReader(`"empty"`))
		r.Header.Set("content-type", "application/json")

		// Execute the handler
		w := httptest.NewRecorder()
		h(w, r)

		// Assertions
		resp := w.Result()
		defer resp.Body.Close()
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Empty(t, respBody)
	})

	t.Run("Option SuccessStatusCode", func(t *testing.T) {
		// Create the handler
		h := UniversalHTTPHandler(
			pingPongHandler,
			UniversalHTTPHandlerOpts[*wrapperspb.StringValue, *wrapperspb.StringValue]{
				SuccessStatusCode: http.StatusCreated,
			},
		)
		require.NotNil(t, h)

		// Create the request
		r := httptest.NewRequest(http.MethodPost, "http://localhost/test", strings.NewReader(`"ping"`))
		r.Header.Set("content-type", "application/json")

		// Execute the handler
		w := httptest.NewRecorder()
		h(w, r)

		// Assertions
		resp := w.Result()
		defer resp.Body.Close()
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("content-type"))

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, `"pong"`, string(respBody))
	})

	t.Run("Option InModifier", func(t *testing.T) {
		// Create the handler
		h := UniversalHTTPHandler(
			pingPongHandler,
			UniversalHTTPHandlerOpts[*wrapperspb.StringValue, *wrapperspb.StringValue]{
				InModifier: func(r *http.Request, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
					if in.GetValue() == "ping" {
						in.Value = "modified"
					}
					return in, nil
				},
			},
		)
		require.NotNil(t, h)

		// Create the request
		r := httptest.NewRequest(http.MethodPost, "http://localhost/test", strings.NewReader(`"ping"`))
		r.Header.Set("content-type", "application/json")

		// Execute the handler
		w := httptest.NewRecorder()
		h(w, r)

		// Assertions
		resp := w.Result()
		defer resp.Body.Close()
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("content-type"))

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, `"🧐"`, string(respBody))
	})

	t.Run("Option OutModifier returns proto", func(t *testing.T) {
		// Create the handler
		h := UniversalHTTPHandler(
			pingPongHandler,
			UniversalHTTPHandlerOpts[*wrapperspb.StringValue, *wrapperspb.StringValue]{
				OutModifier: func(out *wrapperspb.StringValue) (any, error) {
					out.Value = "👻"
					return out, nil
				},
			},
		)
		require.NotNil(t, h)

		// Create the request
		r := httptest.NewRequest(http.MethodPost, "http://localhost/test", strings.NewReader(`"ping"`))
		r.Header.Set("content-type", "application/json")

		// Execute the handler
		w := httptest.NewRecorder()
		h(w, r)

		// Assertions
		resp := w.Result()
		defer resp.Body.Close()
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("content-type"))

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, `"👻"`, string(respBody))
	})

	t.Run("Option OutModifier returns nil", func(t *testing.T) {
		// Create the handler
		h := UniversalHTTPHandler(
			pingPongHandler,
			UniversalHTTPHandlerOpts[*wrapperspb.StringValue, *wrapperspb.StringValue]{
				OutModifier: func(out *wrapperspb.StringValue) (any, error) {
					return nil, nil
				},
			},
		)
		require.NotNil(t, h)

		// Create the request
		r := httptest.NewRequest(http.MethodPost, "http://localhost/test", strings.NewReader(`"ping"`))
		r.Header.Set("content-type", "application/json")

		// Execute the handler
		w := httptest.NewRecorder()
		h(w, r)

		// Assertions
		resp := w.Result()
		defer resp.Body.Close()
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Empty(t, respBody)
	})

	t.Run("Option OutModifier returns JSON data", func(t *testing.T) {
		// Create the handler
		h := UniversalHTTPHandler(
			pingPongHandler,
			UniversalHTTPHandlerOpts[*wrapperspb.StringValue, *wrapperspb.StringValue]{
				OutModifier: func(out *wrapperspb.StringValue) (any, error) {
					return 42, nil
				},
			},
		)
		require.NotNil(t, h)

		// Create the request
		r := httptest.NewRequest(http.MethodPost, "http://localhost/test", strings.NewReader(`"ping"`))
		r.Header.Set("content-type", "application/json")

		// Execute the handler
		w := httptest.NewRecorder()
		h(w, r)

		// Assertions
		resp := w.Result()
		defer resp.Body.Close()
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("content-type"))

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, "42", strings.TrimSpace(string(respBody)))
	})

	t.Run("Option OutModifier returns UniversalHTTPRawResponse", func(t *testing.T) {
		// Create the handler
		h := UniversalHTTPHandler(
			pingPongHandler,
			UniversalHTTPHandlerOpts[*wrapperspb.StringValue, *wrapperspb.StringValue]{
				OutModifier: func(out *wrapperspb.StringValue) (any, error) {
					return &UniversalHTTPRawResponse{
						Body:        []byte("con te partirò"),
						ContentType: "text/plain",
						StatusCode:  http.StatusTeapot,
					}, nil
				},
			},
		)
		require.NotNil(t, h)

		// Create the request
		r := httptest.NewRequest(http.MethodPost, "http://localhost/test", strings.NewReader(`"ping"`))
		r.Header.Set("content-type", "application/json")

		// Execute the handler
		w := httptest.NewRecorder()
		h(w, r)

		// Assertions
		resp := w.Result()
		defer resp.Body.Close()
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusTeapot, resp.StatusCode)
		assert.Equal(t, "text/plain", resp.Header.Get("content-type"))

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, "con te partirò", string(respBody))
	})
}
