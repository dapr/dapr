// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sync"

	cl "actorload/actor/client"
	http_client "actorload/actor/client/http"
	actor_rt "actorload/actor/runtime"

	"github.com/go-chi/chi"
)

const (
	// actorType is Actor Type Name for test.
	actorType = "StateActor"
	// actorStateName is Actor State name.
	actorStateName = "state"
)

type ActorResponse struct {
	Message string `json:"message"`
}

func NewActorResponse(msg string) ActorResponse {
	return ActorResponse{Message: msg}
}

func (e ActorResponse) Encode(w io.Writer) {
	json.NewEncoder(w).Encode(e)
}

type actorInvokeFn func(actorType, actorID string, data []byte, metadata map[string]string) ([]byte, error)

type StateActorServer struct {
	server       *http.Server
	actorClient  cl.ActorClient
	activeActors sync.Map
	entities     []string

	invocationMap map[string]actorInvokeFn
}

func newActorService() *StateActorServer {
	return &StateActorServer{
		server:        nil,
		actorClient:   http_client.NewClient(),
		entities:      []string{},
		invocationMap: map[string]actorInvokeFn{},
	}
}

func (s *StateActorServer) setActorState(actorType, actorID string, data []byte, metadata map[string]string) ([]byte, error) {
	upsertReq := http_client.TransactionalStateOperation{
		Operation: "upsert",
		Request: http_client.TransactionalRequest{
			Key:   actorStateName,
			Value: string(data),
		},
	}

	operations := []http_client.TransactionalStateOperation{upsertReq}
	serialized, err := json.Marshal(operations)
	if err != nil {
		return nil, err
	}

	if err := s.actorClient.SaveStateTransactionally(actorType, actorID, serialized); err != nil {
		return nil, err
	}

	return []byte(""), nil
}

func (s *StateActorServer) getActorState(actorType, actorID string, data []byte, metadata map[string]string) ([]byte, error) {
	data, err := s.actorClient.GetState(actorType, actorID, actorStateName)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (s *StateActorServer) onActivated(actorType, actorID string) error {
	hostname, _ := os.Hostname()
	log.Printf("%s, %s.%s, %s", "Activated", actorType, actorID, hostname)
	return nil
}

func (s *StateActorServer) onDeactivated(actorType, actorID string) error {
	hostname, _ := os.Hostname()
	log.Printf("%s, %s.%s, %s", "Deactivated", actorType, actorID, hostname)
	return nil
}

func (s *StateActorServer) onConfig(w http.ResponseWriter, r *http.Request) {
	config := actor_rt.DaprConfig{
		Entities:                []string{actorType},
		ActorIdleTimeout:        "5m",
		ActorScanInterval:       "10s",
		DrainOngoingCallTimeout: "10s",
		DrainRebalancedActors:   true,
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(config)
}

func (s *StateActorServer) router() http.Handler {
	r := chi.NewRouter()

	s.invocationMap["setActorState"] = s.setActorState
	s.invocationMap["getActorState"] = s.getActorState

	r.Get("/dapr/config", s.onConfig)
	r.Route("/actors/{actorType}/{actorID}", func(r chi.Router) {
		r.Use(actorMiddleware)
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
				s.onActivated(actorType, actorID)
			}

			fn, ok := s.invocationMap[method]
			if !ok {
				w.WriteHeader(http.StatusNotImplemented)
				NewActorResponse(fmt.Sprintf("%s method is not implemented", method)).Encode(w)
				return
			}

			data, err := ioutil.ReadAll(r.Body)
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

		r.Delete("/", func(w http.ResponseWriter, r *http.Request) {
			actorType := r.Context().Value("actorType").(string)
			actorID := r.Context().Value("actorID").(string)
			s.activeActors.Delete(fmt.Sprintf("%s", actorID))
			s.onDeactivated(actorType, actorID)
			w.WriteHeader(http.StatusOK)
			NewActorResponse("deactivated").Encode(w)
		})
	})

	return r
}

func (s *StateActorServer) startServer() {
	s.server = &http.Server{
		Addr:    ":3000",
		Handler: s.router(),
	}

	log.Printf("Start actor service: %s", actorType)

	if err := s.server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func actorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actorType := chi.URLParam(r, "actorType")
		actorID := chi.URLParam(r, "actorID")
		hostname, _ := os.Hostname()

		ctx := context.WithValue(r.Context(), "actorType", actorType)
		ctx = context.WithValue(ctx, "actorID", actorID)

		log.Printf("%s, %s.%s, %s", r.URL.EscapedPath(), actorType, actorID, hostname)
		w.Header().Add("Content-Type", "application/json")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func main() {
	server := newActorService()
	server.startServer()
}
