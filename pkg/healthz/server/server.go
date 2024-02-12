/*
Copyright 2024 The Dapr Authors
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
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/kit/logger"
)

type Handler struct {
	Path   string
	Getter func() ([]byte, error)
}

type Options struct {
	Healthz  healthz.Healthz
	Port     int
	Log      logger.Logger
	Handlers []Handler
}

// Server is a healthz server.
type Server struct {
	log     logger.Logger
	mux     http.Handler
	port    int
	htarget healthz.Target
}

// New returns a new healthz server.
func New(opts Options) *Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if !opts.Healthz.IsReady() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	for _, handler := range opts.Handlers {
		hdl := handler
		mux.Handle(handler.Path, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			data, err := hdl.Getter()
			if err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				writer.Write([]byte(err.Error()))
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, err = writer.Write(data)
			if err != nil {
				opts.Log.Warnf("failed to encode json to response writer: %s", err.Error())
				writer.WriteHeader(http.StatusInternalServerError)
				writer.Write([]byte(err.Error()))
				return
			}
		}))
	}

	return &Server{
		log:     opts.Log,
		port:    opts.Port,
		mux:     mux,
		htarget: opts.Healthz.AddTarget(),
	}
}

// Start starts a net/http server with a healthz endpoint.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return err
	}

	//nolint:gosec
	srv := &http.Server{
		Handler:     s.mux,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	serveErr := make(chan error, 1)
	go func() {
		s.log.Infof("Healthz server is listening on %s", ln.Addr())
		err := srv.Serve(ln)
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	s.htarget.Ready()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		// nop
	}
	s.log.Info("Healthz server is shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	return errors.Join(srv.Shutdown(shutdownCtx), <-serveErr)
}
