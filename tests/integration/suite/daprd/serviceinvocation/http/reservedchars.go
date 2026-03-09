/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reservedChars))
}

type reservedChars struct {
	caller *procdaprd.Daprd
	callee *procdaprd.Daprd
}

func (r *reservedChars) Setup(t *testing.T) []framework.Option {
	// app echoes the path + raw query so we can assert that '?' didn't turn into a query
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(req.URL.Path + "|" + req.URL.RawQuery))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))

	r.callee = procdaprd.New(t, procdaprd.WithAppPort(srv.Port()))
	r.caller = procdaprd.New(t)

	return []framework.Option{
		framework.WithProcesses(srv, r.callee, r.caller),
	}
}

func (r *reservedChars) Run(t *testing.T, ctx context.Context) {
	r.caller.WaitUntilRunning(t, ctx)
	r.callee.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	tests := []struct {
		name         string
		methodSuffix string // what we put after /method/
		expectedBody string // "<Path>|<RawQuery"
	}{
		{
			name:         "hash in method path",
			methodSuffix: "test%23stream",
			expectedBody: "/test#stream|",
		},
		{
			name:         "question mark in method path",
			methodSuffix: "test%3Fstream",
			expectedBody: "/test?stream|",
		},
		{
			name:         "quote in method path",
			methodSuffix: "test%22stream",
			expectedBody: `/test"stream|`,
		},
		{
			name:         "percent in method path",
			methodSuffix: "test%25stream",
			expectedBody: "/test%stream|",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := fmt.Sprintf(
				"http://localhost:%d/v1.0/invoke/%s/method/%s",
				r.caller.HTTPPort(),
				r.callee.AppID(),
				tt.methodSuffix,
			)

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			require.NoError(t, err)

			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, tt.expectedBody, strings.TrimSpace(string(body)))
		})
	}
}
