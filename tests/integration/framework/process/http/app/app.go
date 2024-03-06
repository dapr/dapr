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

package app

import (
	"context"
	nethttp "net/http"
	"sync/atomic"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework/process/http"
)

type Option func(*options)

type App struct {
	http    *http.HTTP
	healthz *atomic.Bool
}

func New(t *testing.T, fopts ...Option) *App {
	t.Helper()

	opts := options{
		subscribe:     "[]",
		config:        "{}",
		initialHealth: true,
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	var healthz atomic.Bool
	healthz.Store(opts.initialHealth)

	httpopts := []http.Option{
		http.WithHandlerFunc("/dapr/config", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			w.Write([]byte(opts.config))
		}),
		http.WithHandlerFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			w.Write([]byte(opts.subscribe))
		}),
		http.WithHandlerFunc("/healthz", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			if healthz.Load() {
				w.WriteHeader(nethttp.StatusOK)
			} else {
				w.WriteHeader(nethttp.StatusServiceUnavailable)
			}
		}),
	}
	httpopts = append(httpopts, opts.handlerFuncs...)

	return &App{
		http:    http.New(t, httpopts...),
		healthz: &healthz,
	}
}

func (a *App) Run(t *testing.T, ctx context.Context) {
	a.http.Run(t, ctx)
}

func (a *App) Cleanup(t *testing.T) {
	a.http.Cleanup(t)
}

func (a *App) Port() int {
	return a.http.Port()
}
