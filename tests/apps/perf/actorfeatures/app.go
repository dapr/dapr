// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/mux"
)

const (
	appPort                        = 3000
	daprV1URL                      = "http://localhost:3500/v1.0"
	actorMethodURLFormat           = daprV1URL + "/actors/%s/%s/%s/%s"
	actorSaveStateURLFormat        = daprV1URL + "/actors/%s/%s/state/"
	actorGetStateURLFormat         = daprV1URL + "/actors/%s/%s/state/%s/"
	defaultActorType               = "testactorfeatures"                  // Actor type must be unique per test app.
	actorTypeEnvName               = "TEST_APP_ACTOR_TYPE"                // Env variable tests can set to change actor type.
	actorReminderPartitionsEnvName = "TEST_APP_ACTOR_REMINDER_PARTITIONS" // Env variable tests can set for reminder partition.
	actorIdleTimeout               = "1h"
	actorScanInterval              = "30s"
	drainOngoingCallTimeout        = "30s"
	drainRebalancedActors          = true
	secondsToWaitInMethod          = 5
)

type daprConfig struct {
	Entities                   []string `json:"entities,omitempty"`
	ActorIdleTimeout           string   `json:"actorIdleTimeout,omitempty"`
	ActorScanInterval          string   `json:"actorScanInterval,omitempty"`
	DrainOngoingCallTimeout    string   `json:"drainOngoingCallTimeout,omitempty"`
	DrainRebalancedActors      bool     `json:"drainRebalancedActors,omitempty"`
	RemindersStoragePartitions int      `json:"remindersStoragePartitions,omitempty"`
}

var registeredActorType = getActorType()

func getActorType() string {
	actorType := os.Getenv(actorTypeEnvName)
	if actorType == "" {
		return defaultActorType
	}

	return actorType
}

func getActorReminderPartitions() int {
	strValue := os.Getenv(actorReminderPartitionsEnvName)
	if strValue == "" {
		return 0
	}

	val, err := strconv.Atoi(strValue)
	if err != nil {
		log.Printf("Invalid number of reminder partitons: %s, %v", strValue, err)
		return 0
	}

	return val
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")
	w.WriteHeader(http.StatusOK)
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	daprConfigResponse := daprConfig{
		[]string{getActorType()},
		actorIdleTimeout,
		actorScanInterval,
		drainOngoingCallTimeout,
		drainRebalancedActors,
		getActorReminderPartitions(),
	}
	log.Printf("Processing dapr request for %s, responding with %v", r.URL.RequestURI(), daprConfigResponse)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(daprConfigResponse)
}

func actorMethodHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing actor method request for %s", r.URL.RequestURI())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

func deactivateActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s actor request for %s", r.Method, r.URL.RequestURI())
	actorType := mux.Vars(r)["actorType"]

	if actorType != registeredActorType {
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

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/dapr/config", configHandler).Methods("GET")

	router.HandleFunc("/actors/{actorType}/{id}/method/{method}", actorMethodHandler).Methods("PUT")
	router.HandleFunc("/actors/{actorType}/{id}/method/{reminderOrTimer}/{method}", actorMethodHandler).Methods("PUT")

	router.HandleFunc("/actors/{actorType}/{id}", deactivateActorHandler).Methods("POST", "DELETE")

	router.HandleFunc("/healthz", healthzHandler).Methods("GET")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Actor App - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
