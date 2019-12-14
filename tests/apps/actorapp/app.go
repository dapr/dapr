// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

const appPort = 3000

const actorMethodURLFormat = "http://localhost:3500/v1.0/actors/%s/%s/method/%s"

const registedActorType = "testactor" // Actor type.
const actorIdleTimeout = "5s"         // Short idle timeout.
const actorScanInterval = "1s"        // Smaller then actorIdleTimeout and short for speedy test.
const drainOngoingCallTimeout = "1s"
const drainBalancedActors = true

type daprActor struct {
	actorType string
	id        string
	value     interface{}
}

// represents a response for the APIs in this app.
type actorLogEntry struct {
	Action    string `json:"action,omitempty"`
	ActorType string `json:"actorType,omitempty"`
	ActorID   string `json:"actorId,omitempty"`
	Timestamp int    `json:"timestamp,omitempty"`
}

type daprConfig struct {
	Entities                []string `json:"entities,omitempty"`
	ActorIdleTimeout        string   `json:"actorIdleTimeout,omitempty"`
	ActorScanInterval       string   `json:"actorScanInterval,omitempty"`
	DrainOngoingCallTimeout string   `json:"drainOngoingCallTimeout,omitempty"`
	DrainBalancedActors     bool     `json:"drainBalancedActors,omitempty"`
}

var daprConfigResponse = daprConfig{
	[]string{registedActorType},
	actorIdleTimeout,
	actorScanInterval,
	drainOngoingCallTimeout,
	drainBalancedActors,
}

var logs = []actorLogEntry{}
var logsMutex = &sync.Mutex{}

var actors = map[string]daprActor{}
var actorsMutex = &sync.Mutex{}

func appendLog(logEntry actorLogEntry) {
	logsMutex.Lock()
	defer logsMutex.Unlock()
	logs = append(logs, logEntry)
}

func getLogs() []actorLogEntry {
	return logs
}

func createActorID(actorType string, id string) string {
	return fmt.Sprintf("%s.%s", actorType, id)
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing dapr request for %s", r.URL.RequestURI())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(getLogs())
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing dapr request for %s", r.URL.RequestURI())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(daprConfigResponse)
}

func actorMethodHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing actor method request for %s", r.URL.RequestURI())

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]
	method := mux.Vars(r)["method"]

	appendLog(actorLogEntry{
		Action:    method,
		ActorType: actorType,
		ActorID:   id,
		Timestamp: epoch(),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

func activateDeactivateActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s actor request for %s", r.Method, r.URL.RequestURI())

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]

	if actorType != registedActorType {
		log.Printf("Unknown actor type: %s", actorType)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	actorID := createActorID(actorType, id)

	actorsMutex.Lock()
	defer actorsMutex.Unlock()

	action := ""
	_, ok := actors[actorID]
	if !ok && r.Method == "POST" {
		action = "activation"
		actors[actorID] = daprActor{
			actorType: actorType,
			id:        actorID,
			value:     nil,
		}
	}

	if ok && r.Method == "DELETE" {
		action = "deactivation"
		delete(actors, actorID)
	}

	appendLog(actorLogEntry{
		Action:    action,
		ActorType: actorType,
		ActorID:   id,
		Timestamp: epoch(),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

// calls Dapr's Actor method: simulating actor client call.
// nolint:gosec
func testCallActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s test request for %s", r.Method, r.URL.RequestURI())

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]
	method := mux.Vars(r)["method"]

	invokeURL := fmt.Sprintf(actorMethodURLFormat, actorType, id, method)
	log.Printf("Invoking %s", invokeURL)

	res, err := http.Post(invokeURL, "application/json", bytes.NewBuffer([]byte{}))
	if err != nil {
		log.Printf("Could not test actor: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Printf("Could not read actor's test response: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

// epoch returns the current unix epoch timestamp
func epoch() int {
	return (int)(time.Now().UnixNano() / 1000000)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/dapr/config", configHandler).Methods("GET")
	router.HandleFunc("/actors/{actorType}/{id}/method/{method}", actorMethodHandler).Methods("PUT")
	router.HandleFunc("/actors/{actorType}/{id}", activateDeactivateActorHandler).Methods("POST", "DELETE")
	router.HandleFunc("/test/{actorType}/{id}/method/{method}", testCallActorHandler).Methods("POST")
	router.HandleFunc("/test/logs", logsHandler).Methods("GET")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Actor App - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
