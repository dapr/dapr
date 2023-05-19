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

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	cl "github.com/dapr/dapr/tests/apps/actorload/pkg/actor/client"
	httpClient "github.com/dapr/dapr/tests/apps/actorload/pkg/actor/client/http"
	rt "github.com/dapr/dapr/tests/apps/actorload/pkg/actor/runtime"

	"github.com/go-chi/chi/v5"
)

type ActorActivationHandler func(actorType, actorID string) error

type ActorInvokeFn func(actorType, actorID string, data []byte, metadata map[string]string) ([]byte, error)

type ActorService struct {
	address string
	server  *http.Server

	activeActors sync.Map
	actorClient  cl.ActorClient
	config       rt.DaprConfig

	activationHandler   ActorActivationHandler
	deactivationHandler ActorActivationHandler

	invocationMap map[string]ActorInvokeFn
}

func NewActorService(port int, config *rt.DaprConfig) *ActorService {
	var daprConfig rt.DaprConfig

	if config == nil {
		daprConfig = rt.DaprConfig{
			Entities:                []string{},
			ActorIdleTimeout:        "60m",
			ActorScanInterval:       "10s",
			DrainOngoingCallTimeout: "10s",
			DrainRebalancedActors:   true,
		}
	} else {
		daprConfig = *config
	}

	return &ActorService{
		address:             fmt.Sprintf("127.0.0.1:%d", port),
		server:              nil,
		actorClient:         httpClient.NewClient(),
		invocationMap:       map[string]ActorInvokeFn{},
		config:              daprConfig,
		activationHandler:   nil,
		deactivationHandler: nil,
	}
}

func (s *ActorService) setActorTypes(actorTypes []string) {
	s.config.Entities = actorTypes
}

func (s *ActorService) SetActivationHandler(handler ActorActivationHandler) {
	s.activationHandler = handler
}

func (s *ActorService) SetDeactivationHandler(handler ActorActivationHandler) {
	s.deactivationHandler = handler
}

func (s *ActorService) onConfig(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(s.config)
}

func (s *ActorService) onHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

func (s *ActorService) router() http.Handler {
	r := chi.NewRouter()
	r.Get("/dapr/config", s.onConfig)
	r.Get("/healthz", s.onHealthz)

	r.Route("/actors/{actorType}/{actorID}", func(r chi.Router) {
		r.Use(actorMiddleware)

		r.Delete("/", func(w http.ResponseWriter, r *http.Request) {
			actorType := r.Context().Value("actorType").(string)
			actorID := r.Context().Value("actorID").(string)

			s.activeActors.Delete(fmt.Sprintf("%s", actorID))

			if s.deactivationHandler != nil {
				s.deactivationHandler(actorType, actorID)
			}

			w.WriteHeader(http.StatusOK)
			NewActorResponse("deactivated").Encode(w)
		})

		r.Put("/method/{method}", func(w http.ResponseWriter, r *http.Request) {
			method := chi.URLParam(r, "method")

			actorType := r.Context().Value("actorType").(string)
			actorID := r.Context().Value("actorID").(string)

			if method == "" {
				w.WriteHeader(http.StatusBadRequest)
				NewActorResponse("method is not given").Encode(w)
				return
			}

			if actorType == "" || actorID == "" {
				w.WriteHeader(http.StatusBadRequest)
				NewActorResponse("actorType or actorID is not given").Encode(w)
				return
			}

			// is this first call? then try to activate actor.
			_, loaded := s.activeActors.LoadOrStore(actorID, actorType)
			if !loaded {
				if s.activationHandler != nil {
					s.activationHandler(actorType, actorID)
				}
			}

			fn, ok := s.invocationMap[method]
			if !ok {
				w.WriteHeader(http.StatusNotImplemented)
				NewActorResponse(fmt.Sprintf("%s method is not implemented", method)).Encode(w)
				return
			}

			data, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				NewActorResponse(err.Error()).Encode(w)
				return
			}

			resp, err := fn(actorType, actorID, data, map[string]string{})
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				NewActorResponse(err.Error()).Encode(w)
				return
			}

			w.WriteHeader(http.StatusOK)
			w.Write(resp)
		})
	})

	return r
}

func (s *ActorService) AddActorMethod(name string, fn ActorInvokeFn) error {
	if _, ok := s.invocationMap[name]; ok {
		return errors.New("method exists")
	}
	s.invocationMap[name] = fn
	return nil
}

func (s *ActorService) StartServer() {
	s.server = &http.Server{
		Addr:    s.address,
		Handler: s.router(),
	}

	actorTypes := strings.Join(s.config.Entities, ", ")
	log.Printf("Listening to %s, Actor Types: %s", s.address, actorTypes)

	if err := s.server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func actorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actorType := chi.URLParam(r, "actorType")
		actorID := chi.URLParam(r, "actorID")

		ctx := context.WithValue(r.Context(), "actorType", actorType)
		ctx = context.WithValue(ctx, "actorID", actorID)

		w.Header().Add("Content-Type", "application/json")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
