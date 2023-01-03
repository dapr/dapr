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

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/mux"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort                 = 22222
	daprV1URL               = "http://localhost:3500/v1.0"
	actorMethodURLFormat    = daprV1URL + "/actors/%s/%s/method/%s"
	defaultActorType        = "reentrantActor"
	actorIdleTimeout        = "1h"
	actorScanInterval       = "30s"
	drainOngoingCallTimeout = "30s"
	drainRebalancedActors   = true
	reentrantMethod         = "reentrantMethod"
	standardMethod          = "standardMethod"
)

// represents a response for the APIs in this app.
type actorLogEntry struct {
	Action    string `json:"action,omitempty"`
	ActorType string `json:"actorType,omitempty"`
	ActorID   string `json:"actorId,omitempty"`
}

type daprConfig struct {
	Entities                []string                `json:"entities,omitempty"`
	ActorIdleTimeout        string                  `json:"actorIdleTimeout,omitempty"`
	ActorScanInterval       string                  `json:"actorScanInterval,omitempty"`
	DrainOngoingCallTimeout string                  `json:"drainOngoingCallTimeout,omitempty"`
	DrainRebalancedActors   bool                    `json:"drainRebalancedActors,omitempty"`
	Reentrancy              config.ReentrancyConfig `json:"reentrancy,omitempty"`
	EntitiesConfig          []config.EntityConfig   `json:"entitiesConfig,omitempty"`
}

type reentrantRequest struct {
	Calls []actorCall `json:"calls,omitempty"`
}

type actorCall struct {
	ActorID   string `json:"id"`
	ActorType string `json:"type"`
	Method    string `json:"method"`
}

var httpClient = utils.NewHTTPClient()

var (
	actorLogs      = []actorLogEntry{}
	actorLogsMutex = &sync.Mutex{}
	maxStackDepth  = 5
)

var daprConfigResponse = daprConfig{
	Entities:                []string{defaultActorType},
	ActorIdleTimeout:        actorIdleTimeout,
	ActorScanInterval:       actorScanInterval,
	DrainOngoingCallTimeout: drainOngoingCallTimeout,
	DrainRebalancedActors:   drainRebalancedActors,
	Reentrancy:              config.ReentrancyConfig{Enabled: false},
	EntitiesConfig: []config.EntityConfig{
		{
			Entities: []string{defaultActorType},
			Reentrancy: config.ReentrancyConfig{
				Enabled:       true,
				MaxStackDepth: &maxStackDepth,
			},
		},
	},
}

func resetLogs() {
	actorLogsMutex.Lock()
	defer actorLogsMutex.Unlock()

	actorLogs = []actorLogEntry{}
}

func appendLog(actorType string, actorID string, action string) {
	logEntry := actorLogEntry{
		Action:    action,
		ActorType: actorType,
		ActorID:   actorID,
	}

	actorLogsMutex.Lock()
	defer actorLogsMutex.Unlock()
	actorLogs = append(actorLogs, logEntry)
}

func getLogs() []actorLogEntry {
	return actorLogs
}

// indexHandler is the handler for root path.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing dapr %s request for %s", r.Method, r.URL.RequestURI())
	if r.Method == http.MethodDelete {
		resetLogs()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(getLogs())
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing dapr request for %s, responding with %v", r.URL.RequestURI(), daprConfigResponse)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(daprConfigResponse)
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

func actorTestCallHandler(w http.ResponseWriter, r *http.Request) {
	log.Print("Handling actor test call")

	body, err := io.ReadAll(r.Body)
	defer r.Body.Close()

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var reentrantReq reentrantRequest
	json.Unmarshal(body, &reentrantReq)

	if len(reentrantReq.Calls) == 0 {
		log.Print("No calls in test chain")
		return
	}

	nextCall, nextBody := advanceCallStackForNextRequest(reentrantReq)
	req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf(actorMethodURLFormat, nextCall.ActorType, nextCall.ActorID, nextCall.Method), bytes.NewReader(nextBody))

	res, err := httpClient.Do(req)
	if err != nil {
		w.Write([]byte(err.Error()))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	respBody, err := io.ReadAll(res.Body)
	defer res.Body.Close()

	if err != nil {
		w.Write([]byte(err.Error()))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Write(respBody)
}

func actorMethodHandler(w http.ResponseWriter, r *http.Request) {
	log.Print("Handling actor method call.")
	method := mux.Vars(r)["method"]

	switch method {
	case reentrantMethod:
		reentrantCallHandler(w, r)
	case standardMethod:
		fallthrough
	default:
		standardHandler(w, r)
	}
}

func reentrantCallHandler(w http.ResponseWriter, r *http.Request) {
	log.Print("Handling reentrant call")
	appendLog(mux.Vars(r)["actorType"], mux.Vars(r)["id"], fmt.Sprintf("Enter %s", mux.Vars(r)["method"]))
	body, err := io.ReadAll(r.Body)
	defer r.Body.Close()

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var reentrantReq reentrantRequest
	json.Unmarshal(body, &reentrantReq)

	if len(reentrantReq.Calls) == 0 {
		log.Print("End of call chain")
		return
	}

	nextCall, nextBody := advanceCallStackForNextRequest(reentrantReq)

	log.Printf("Next call: %s on %s.%s", nextCall.Method, nextCall.ActorType, nextCall.ActorID)

	url := fmt.Sprintf(actorMethodURLFormat, nextCall.ActorType, nextCall.ActorID, nextCall.Method)
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(nextBody))

	reentrancyID := r.Header.Get("Dapr-Reentrancy-Id")
	req.Header.Add("Dapr-Reentrancy-Id", reentrancyID)

	resp, err := httpClient.Do(req)

	log.Printf("Call status: %d\n", resp.StatusCode)
	if err != nil || resp.StatusCode == http.StatusInternalServerError {
		w.WriteHeader(http.StatusInternalServerError)
		appendLog(mux.Vars(r)["actorType"], mux.Vars(r)["id"], fmt.Sprintf("Error %s", mux.Vars(r)["method"]))
	} else {
		appendLog(mux.Vars(r)["actorType"], mux.Vars(r)["id"], fmt.Sprintf("Exit %s", mux.Vars(r)["method"]))
	}
	defer resp.Body.Close()
}

func standardHandler(w http.ResponseWriter, r *http.Request) {
	log.Print("Handling standard call")
	appendLog(mux.Vars(r)["actorType"], mux.Vars(r)["id"], fmt.Sprintf("Enter %s", mux.Vars(r)["method"]))
	appendLog(mux.Vars(r)["actorType"], mux.Vars(r)["id"], fmt.Sprintf("Exit %s", mux.Vars(r)["method"]))
}

func advanceCallStackForNextRequest(req reentrantRequest) (actorCall, []byte) {
	nextCall := req.Calls[0]
	log.Printf("Current call: %v\n", nextCall)
	nextReq := req
	nextReq.Calls = nextReq.Calls[1:]
	log.Printf("Next req: %v\n", nextReq)
	nextBody, _ := json.Marshal(nextReq)
	return nextCall, nextBody
}

// appRouter initializes restful api router.
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/dapr/config", configHandler).Methods("GET")

	router.HandleFunc("/actors/{actorType}/{id}/method/{method}", actorMethodHandler).Methods("PUT")
	router.HandleFunc("/test/actors/{actorType}/{id}/method/{method}", actorTestCallHandler).Methods("POST")

	router.HandleFunc("/test/logs", logsHandler).Methods("GET", "DELETE")
	router.HandleFunc("/healthz", healthzHandler).Methods("GET")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Actor App - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
