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
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/dapr/kit/logger"
)

// Server is the interface for the healthz server.
type Server interface {
	Run(context.Context, int) error
	Ready()
	NotReady()
}

type server struct {
	ready *atomic.Bool
	log   logger.Logger
}

// NewServer returns a new healthz server.
func NewServer(log logger.Logger) Server {
	return &server{
		log:   log,
		ready: &atomic.Bool{},
	}
}

// Ready sets a ready state for the endpoint handlers.
func (s *server) Ready() {
	s.ready.Store(true)
}

// NotReady sets a not ready state for the endpoint handlers.
func (s *server) NotReady() {
	s.ready.Store(false)
}

// Run starts a net/http server with a healthz endpoint.
func (s *server) Run(ctx context.Context, port int) error {
	router := http.NewServeMux()
	router.Handle("/healthz", s.healthz())

	//nolint:gosec
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: router,
	}

	go func() {
		select {
		case <-ctx.Done():
			s.log.Info("Healthz server is shutting down")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			defer cancel()
			err := srv.Shutdown(shutdownCtx)
			if err != nil {
				s.log.Errorf("Error while shutting down healthz server: %v", err)
			}
		}
	}()

	s.log.Infof("Healthz server is listening on %s", srv.Addr)

	// Blocking call
	err := srv.ListenAndServe()
	if err != http.ErrServerClosed {
		return err
	}

	return nil
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
