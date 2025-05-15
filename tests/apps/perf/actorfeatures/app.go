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

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	chi "github.com/go-chi/chi/v5"

	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort                 = 3000
	daprV1URL               = "http://localhost:3500/v1.0"
	actorMethodURLFormat    = daprV1URL + "/actors/%s/%s/%s/%s"
	actorSaveStateURLFormat = daprV1URL + "/actors/%s/%s/state/"
	actorGetStateURLFormat  = daprV1URL + "/actors/%s/%s/state/%s/"
	defaultActorType        = "testactorfeatures"   // Actor type must be unique per test app.
	actorTypeEnvName        = "TEST_APP_ACTOR_TYPE" // Env variable tests can set to change actor type.
	actorIdleTimeout        = "1h"
	drainOngoingCallTimeout = "30s"
	drainRebalancedActors   = true
	secondsToWaitInMethod   = 5
)

var httpClient = utils.NewHTTPClient()

type daprConfig struct {
	Entities                []string `json:"entities,omitempty"`
	ActorIdleTimeout        string   `json:"actorIdleTimeout,omitempty"`
	DrainOngoingCallTimeout string   `json:"drainOngoingCallTimeout,omitempty"`
	DrainRebalancedActors   bool     `json:"drainRebalancedActors,omitempty"`
}

var registeredActorType = map[string]bool{}

var daprConfigResponse = daprConfig{
	getActorType(),
	actorIdleTimeout,
	drainOngoingCallTimeout,
	drainRebalancedActors,
}

func getActorType() []string {
	actorType := os.Getenv(actorTypeEnvName)
	if actorType == "" {
		registeredActorType[defaultActorType] = true
		return []string{defaultActorType}
	}

	actorTypes := strings.Split(actorType, ",")
	for _, tp := range actorTypes {
		registeredActorType[tp] = true
	}
	return actorTypes
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")
	w.WriteHeader(http.StatusOK)
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing Dapr request for %s, responding with %#v", r.URL.RequestURI(), daprConfigResponse)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(daprConfigResponse)
}

func deactivateActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s actor request for %s", r.Method, r.URL.RequestURI())
	actorType := chi.URLParam(r, "actorType")

	if !registeredActorType[actorType] {
		log.Printf("Unknown actor type: %s", actorType)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

var actorReminderCount atomic.Uint32

func actorReminderHandler(w http.ResponseWriter, r *http.Request) {
	actorReminderCount.Add(1)
}

func actorRemindersCountHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(actorReminderCount.Load())
}

func createActorReminder(w http.ResponseWriter, r *http.Request) {
	log.Printf("Creating actor reminder for %s", r.URL.RequestURI())

	actorType := chi.URLParam(r, "actorType")
	actorID := chi.URLParam(r, "id")
	reminderName := chi.URLParam(r, "reminderName")

	endpoint := fmt.Sprintf("http://127.0.0.1:3500/v1.0/actors/%s/%s/reminders/%s", actorType, actorID, reminderName)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, r.Body)
	if err != nil {
		log.Printf("Error creating request: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Error calling Dapr: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func actorMethodHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing actor method request for %s", r.URL.RequestURI())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

func invokeActor(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s actor request for %s", r.Method, r.URL.RequestURI())

	actorType := chi.URLParam(r, "actorType")
	actorID := chi.URLParam(r, "id")
	method := chi.URLParam(r, "method")

	endpoint := fmt.Sprintf("http://127.0.0.1:3500/v1.0/actors/%s/%s/method/%s", actorType, actorID, method)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, r.Body)
	if err != nil {
		log.Printf("Error creating request: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Error calling Dapr: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := chi.NewRouter()

	router.Get("/", indexHandler)
	router.Get("/dapr/config", configHandler)

	router.HandleFunc("/actors/{actorType}/{id}/method/{method}", actorMethodHandler)
	router.Get("/actors/{actorType}/{id}/method/{method}", invokeActor)
	router.HandleFunc("/actors/{actorType}/{id}/method/remind/{reminderName}", actorReminderHandler)
	router.HandleFunc("/actors/{actorType}/{id}/reminders/{reminderName}", createActorReminder)
	router.HandleFunc("/remindersCount", actorRemindersCountHandler)

	router.Post("/actors/{actorType}/{id}", deactivateActorHandler)
	router.Delete("/actors/{actorType}/{id}", deactivateActorHandler)

	router.Get("/healthz", healthzHandler)

	router.HandleFunc("/test", fortioTestHandler)

	return router
}

func main() {
	log.Printf("Actor App - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
