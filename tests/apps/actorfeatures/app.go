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
	"os"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

const (
	appPort              = 3000
	daprV1URL            = "http://localhost:3500/v1.0"
	actorMethodURLFormat = daprV1URL + "/actors/%s/%s/%s/%s"

	registedActorType       = "testactorfeatures" // Actor type must be unique per test app.
	actorIdleTimeout        = "1h"
	actorScanInterval       = "30s"
	drainOngoingCallTimeout = "30s"
	drainBalancedActors     = true
	secondsToWaitInMethod   = 5
)

type daprActor struct {
	actorType string
	id        string
	value     int
}

// represents a response for the APIs in this app.
type actorLogEntry struct {
	Action         string `json:"action,omitempty"`
	ActorType      string `json:"actorType,omitempty"`
	ActorID        string `json:"actorId,omitempty"`
	StartTimestamp int    `json:"startTimestamp,omitempty"`
	EndTimestamp   int    `json:"endTimestamp,omitempty"`
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

type daprActorResponse struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}

// request for timer or reminder.
type timerReminderRequest struct {
	Data     string `json:"data,omitempty"`
	DueTime  string `json:"dueTime,omitempty"`
	Period   string `json:"period,omitempty"`
	Callback string `json:"callback,omitempty"`
}

// requestResponse represents a request or response for the APIs in this app.
type response struct {
	ActorType string `json:"actorType,omitempty"`
	ActorID   string `json:"actorId,omitempty"`
	Method    string `json:"method,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
	Message   string `json:"message,omitempty"`
}

var actorLogs = []actorLogEntry{}
var actorLogsMutex = &sync.Mutex{}

var actors sync.Map

func resetLogs() {
	actorLogsMutex.Lock()
	defer actorLogsMutex.Unlock()

	actorLogs = []actorLogEntry{}
}

func appendLog(actorType string, actorID string, action string, start int) {
	logEntry := actorLogEntry{
		Action:         action,
		ActorType:      actorType,
		ActorID:        actorID,
		StartTimestamp: start,
		EndTimestamp:   epoch(),
	}

	actorLogsMutex.Lock()
	defer actorLogsMutex.Unlock()
	actorLogs = append(actorLogs, logEntry)
}

func getLogs() []actorLogEntry {
	return actorLogs
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
	log.Printf("Processing dapr %s request for %s", r.Method, r.URL.RequestURI())
	if r.Method == "DELETE" {
		resetLogs()
	}

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

	start := epoch()

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]
	method := mux.Vars(r)["method"]
	reminderOrTimer := mux.Vars(r)["reminderOrTimer"] != ""

	hostname, err := os.Hostname()
	var data []byte
	if method == "hostname" {
		data = []byte(hostname)
	} else {
		// Sleep for all calls, except timer and reminder.
		if !reminderOrTimer {
			time.Sleep(secondsToWaitInMethod * time.Second)
		}
		data, err = json.Marshal(response{
			actorType,
			id,
			method,
			start,
			epoch(),
			"",
		})
	}

	if err != nil {
		fmt.Printf("Error: %v", err.Error())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	appendLog(actorType, id, method, start)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(daprActorResponse{
		Data: data,
	})
}

func activateDeactivateActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s actor request for %s", r.Method, r.URL.RequestURI())

	start := epoch()

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]

	if actorType != registedActorType {
		log.Printf("Unknown actor type: %s", actorType)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	actorID := createActorID(actorType, id)

	action := ""
	_, ok := actors.Load(actorID)
	if !ok && r.Method == "POST" {
		action = "activation"
		actors.Store(actorID, daprActor{
			actorType: actorType,
			id:        actorID,
			value:     epoch(),
		})
	}

	if ok && r.Method == "DELETE" {
		action = "deactivation"
		actors.Delete(actorID)
	}

	appendLog(actorType, id, action, start)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

// calls Dapr's Actor method/timer/reminder: simulating actor client call.
// nolint:gosec
func testCallActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s test request for %s", r.Method, r.URL.RequestURI())

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]
	callType := mux.Vars(r)["callType"]
	method := mux.Vars(r)["method"]

	url := fmt.Sprintf(actorMethodURLFormat, actorType, id, callType, method)

	var req interface{}
	switch callType {
	case "method":
		// NO OP
	case "timers":
		req = timerReminderRequest{
			Data:    "timerdata",
			DueTime: "1s",
			Period:  "1s",
		}
	case "reminders":
		req = timerReminderRequest{
			Data:    "reminderdata",
			DueTime: "1s",
			Period:  "1s",
		}
	}

	body, err := httpCall(r.Method, url, req)
	if err != nil {
		log.Printf("Could not read actor's test response: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if len(body) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	var response daprActorResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		log.Printf("Could parse actor's test response: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Write(response.Data)
}

func testCallMetadataHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s test request for %s", r.Method, r.URL.RequestURI())

	metadataURL := fmt.Sprintf("%s/metadata", daprV1URL)
	body, err := httpCall(r.Method, metadataURL, nil)
	if err != nil {
		log.Printf("Could not read metadata response: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

func httpCall(method string, url string, requestBody interface{}) ([]byte, error) {
	var body []byte
	var err error

	if requestBody != nil {
		body, err = json.Marshal(requestBody)
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer res.Body.Close()

	resBody, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	return resBody, nil
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// epoch returns the current unix epoch timestamp
func epoch() int {
	return (int)(time.Now().UTC().UnixNano() / 1000000)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/dapr/config", configHandler).Methods("GET")
	router.HandleFunc("/actors/{actorType}/{id}/method/{method}", actorMethodHandler).Methods("PUT")
	router.HandleFunc("/actors/{actorType}/{id}/method/{reminderOrTimer}/{method}", actorMethodHandler).Methods("PUT")
	router.HandleFunc("/actors/{actorType}/{id}", activateDeactivateActorHandler).Methods("POST", "DELETE")
	router.HandleFunc("/test/{actorType}/{id}/{callType}/{method}", testCallActorHandler).Methods("POST", "DELETE")
	router.HandleFunc("/test/logs", logsHandler).Methods("GET")
	router.HandleFunc("/test/metadata", testCallMetadataHandler).Methods("GET")
	router.HandleFunc("/test/logs", logsHandler).Methods("DELETE")
	router.HandleFunc("/healthz", healthzHandler).Methods("GET")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Actor App - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
