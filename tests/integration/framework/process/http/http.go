/*
Copyright 2023 The Dapr Authors
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
	"errors"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/ports"
)

// Option is a function that configures the process.
type Option func(*options)

// HTTP is a HTTP server that can be used in integration tests.
type HTTP struct {
	listener net.Listener
	server   *http.Server
	srvErrCh chan error
}

func New(t *testing.T, fopts ...Option) *HTTP {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	require.False(t, len(opts.handlerFuncs) > 0 && opts.handler != nil,
		"handler and handlerFuncs are mutually exclusive, handlerFuncs: %d, handler: %v",
		len(opts.handlerFuncs), opts.handler)

	if opts.handler == nil {
		handler := http.NewServeMux()
		for path, fn := range opts.handlerFuncs {
			handler.HandleFunc(path, fn)
		}
		opts.handler = handler
	}

	var lis net.Listener
	if opts.port != nil {
		var err error
		lis, err = net.Listen("tcp", "localhost:"+strconv.Itoa(*opts.port))
		require.NoError(t, err)
	} else {
		lis = ports.Reserve(t, 1).Listener(t)
	}

	return &HTTP{
		listener: lis,
		srvErrCh: make(chan error, 1),
		server: &http.Server{
			ReadHeaderTimeout: time.Second,
			Handler:           opts.handler,
			TLSConfig:         opts.tlsConfig,
		},
	}
}

func (h *HTTP) Port() int {
	return h.listener.Addr().(*net.TCPAddr).Port
}

func (h *HTTP) Run(t *testing.T, ctx context.Context) {
	h.server.BaseContext = func(_ net.Listener) context.Context {
		return ctx
	}

	go func() {
		var err error
		if h.server.TLSConfig != nil {
			err = h.server.ServeTLS(h.listener, "", "")
		} else {
			err = h.server.Serve(h.listener)
		}
		if !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.DeadlineExceeded) {
			h.srvErrCh <- err
		} else {
			h.srvErrCh <- nil
		}
	}()
}

func (h *HTTP) Cleanup(t *testing.T) {
	//nolint:usetesting
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	require.NoError(t, errors.Join(h.server.Shutdown(ctx), <-h.srvErrCh))
}
