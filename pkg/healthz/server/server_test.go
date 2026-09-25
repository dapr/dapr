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

package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/kit/logger"
)

func TestListenAddressIsHonored(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	srv := New(Options{
		Log:           logger.NewLogger(t.Name()),
		ListenAddress: "127.0.0.1",
		Port:          port,
		Healthz:       healthz.New(),
	})

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", port), nil)
		if !assert.NoError(c, rerr) {
			return
		}
		resp, rerr := http.DefaultClient.Do(req)
		if !assert.NoError(c, rerr) {
			return
		}
		resp.Body.Close()
		assert.Equal(c, http.StatusOK, resp.StatusCode)
	}, time.Second*5, time.Millisecond*10)

	cancel()
	require.NoError(t, <-errCh)
}
