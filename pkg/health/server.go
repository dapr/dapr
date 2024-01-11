/*
Copyright 2021 The Dapr Authors
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

package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/dapr/kit/logger"
)

// Server is the interface for the healthz server.
type Server interface {
	Run(context.Context, int) error
	Ready()
}

type server struct {
	ready        atomic.Bool
	targetsReady atomic.Int64
	targets      int64
	router       http.Handler
	log          logger.Logger
}

type Options struct {
	RouterOptions []RouterOptions
	Targets       *int
	Log           logger.Logger
}

type RouterOptions func(log logger.Logger) (string, http.Handler)

func NewRouterOptions(path string, handler http.Handler) RouterOptions {
	return func(log logger.Logger) (string, http.Handler) {
		return path, handler
	}
}

func NewJSONDataRouterOptions[T any](path string, getter func() (T, error)) RouterOptions {
	return func(log logger.Logger) (string, http.Handler) {
		return path, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			data, err := getter()
			if err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				writer.Write([]byte(err.Error()))
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			err = json.NewEncoder(writer).Encode(data)
			if err != nil {
				log.Warnf("failed to encode json to response writer: %s", err.Error())
				writer.WriteHeader(http.StatusInternalServerError)
				writer.Write([]byte(err.Error()))
				return
			}
		})
	}
}

// NewServer returns a new healthz server.
func NewServer(opts Options) Server {
	targets := 1
	if opts.Targets != nil {
		targets = *opts.Targets
	}

	s := &server{
		log:     opts.Log,
		targets: int64(targets),
	}

	router := http.NewServeMux()
	router.Handle("/healthz", s.healthz())
	// add public handlers to the router
	for _, option := range opts.RouterOptions {
		path, handler := option(s.log)
		router.Handle(path, handler)
	}
	s.router = router
	return s
}

// Ready sets a ready state for the endpoint handlers.
func (s *server) Ready() {
	s.targetsReady.Add(1)
	if s.targetsReady.Load() >= s.targets {
		s.ready.Store(true)
	}
}

// Run starts a net/http server with a healthz endpoint.
func (s *server) Run(ctx context.Context, port int) error {
	//nolint:gosec
	srv := &http.Server{
		Addr:        fmt.Sprintf(":%d", port),
		Handler:     s.router,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	serveErr := make(chan error, 1)
	go func() {
		s.log.Infof("Healthz server is listening on %s", srv.Addr)
		err := srv.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

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

// healthz is a health endpoint handler.
func (s *server) healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var status int
		if s.ready.Load() {
			status = http.StatusOK
		} else {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
	})
}
