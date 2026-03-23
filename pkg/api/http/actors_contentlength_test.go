package http

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// TestActorResponseContentLengthBodyMatch verifies that Content-Length from
// upstream response headers is NOT blindly forwarded to the client when daprd
// rebuilds the response body. Instead, Go's ResponseWriter should compute the
// correct Content-Length from the actual data written.
//
// Regression test for https://github.com/dapr/dapr/issues/9668
func TestActorResponseContentLengthBodyMatch(t *testing.T) {
	tests := []struct {
		name                  string
		upstreamContentLength string
		bodyToWrite           []byte
		expectedBody          string
	}{
		{
			name:                  "null body - upstream CL matches",
			upstreamContentLength: "4",
			bodyToWrite:           []byte("null"),
			expectedBody:          "null",
		},
		{
			name:                  "upstream CL larger than actual body",
			upstreamContentLength: "100",
			bodyToWrite:           []byte("short"),
			expectedBody:          "short",
		},
		{
			name:                  "upstream CL smaller than actual body",
			upstreamContentLength: "2",
			bodyToWrite:           []byte("much longer body"),
			expectedBody:          "much longer body",
		},
		{
			name:                  "empty body with non-zero upstream CL",
			upstreamContentLength: "4",
			bodyToWrite:           nil,
			expectedBody:          "",
		},
		{
			name:                  "false JSON value",
			upstreamContentLength: "5",
			bodyToWrite:           []byte("false"),
			expectedBody:          "false",
		},
		{
			name:                  "large body",
			upstreamContentLength: "8192",
			bodyToWrite:           []byte(strings.Repeat("x", 8192)),
			expectedBody:          strings.Repeat("x", 8192),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Build internal metadata simulating upstream response headers.
			md := map[string]*internalv1pb.ListStringValue{
				"content-length": {Values: []string{tc.upstreamContentLength}},
				"content-type":   {Values: []string{"application/json"}},
			}

			// Simulate what onDirectActorMessage does:
			// 1. Forward upstream headers to the ResponseWriter
			// 2. Set content-type explicitly
			// 3. Call respondWithData with the body
			rec := httptest.NewRecorder()
			h := rec.Header()
			invokev1.InternalMetadataToHTTPHeader(t.Context(), md, h.Add)
			h.Set("content-type", "application/json")

			respondWithData(rec, http.StatusOK, tc.bodyToWrite)

			resp := rec.Result()
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedBody, string(body),
				"response body should match expected content")

			// The critical check: Content-Length must match actual body
			// length. Before the fix, Content-Length could be the stale
			// upstream value rather than the actual body length.
			if cl := resp.Header.Get("Content-Length"); cl != "" {
				clInt, err := strconv.Atoi(cl)
				require.NoError(t, err)
				assert.Equal(t, len(body), clInt,
					fmt.Sprintf("Content-Length header (%d) must match actual body length (%d)", clInt, len(body)))
			}
		})
	}
}
