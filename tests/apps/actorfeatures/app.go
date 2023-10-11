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
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/dapr/dapr/tests/apps/utils"

	"github.com/gorilla/mux"
)

const (
	daprBaseURLFormat               = "http://localhost:%d/v1.0"
	actorMethodURLFormat            = daprBaseURLFormat + "/actors/%s/%s/%s/%s"
	actorSaveStateURLFormat         = daprBaseURLFormat + "/actors/%s/%s/state/"
	actorGetStateURLFormat          = daprBaseURLFormat + "/actors/%s/%s/state/%s/"
	actorReminderURLFormat          = daprBaseURLFormat + "/actors/%s/%s/%s/%s"
	defaultActorType                = "testactorfeatures"                   // Actor type must be unique per test app.
	actorTypeEnvName                = "TEST_APP_ACTOR_TYPE"                 // To set to change actor type.
	actorRemindersPartitionsEnvName = "TEST_APP_ACTOR_REMINDERS_PARTITIONS" // To set actor type partition count.
	actorIdleTimeout                = "1h"
	actorScanInterval               = "30s"
	drainOngoingCallTimeout         = "30s"
	drainRebalancedActors           = true
	secondsToWaitInMethod           = 5
)

var (
	appPort      = 3000
	daprHTTPPort = 3500
	httpClient   = utils.NewHTTPClient()
)

func init() {
	p := os.Getenv("DAPR_HTTP_PORT")
	if p != "" && p != "0" {
		daprHTTPPort, _ = strconv.Atoi(p)
	}
	p = os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
}

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
	Entities                   []string `json:"entities,omitempty"`
	ActorIdleTimeout           string   `json:"actorIdleTimeout,omitempty"`
	ActorScanInterval          string   `json:"actorScanInterval,omitempty"`
	DrainOngoingCallTimeout    string   `json:"drainOngoingCallTimeout,omitempty"`
	DrainRebalancedActors      bool     `json:"drainRebalancedActors,omitempty"`
	RemindersStoragePartitions int      `json:"remindersStoragePartitions,omitempty"`
}

// response object from an actor invocation request
type daprActorResponse struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}

// request for timer or reminder.
type timerReminderRequest struct {
	OldName   string `json:"oldName,omitempty"`
	ActorType string `json:"actorType,omitempty"`
	ActorID   string `json:"actorID,omitempty"`
	NewName   string `json:"newName,omitempty"`
	Data      string `json:"data,omitempty"`
	DueTime   string `json:"dueTime,omitempty"`
	Period    string `json:"period,omitempty"`
	TTL       string `json:"ttl,omitempty"`
	Callback  string `json:"callback,omitempty"`
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

// copied from actors.go for test purposes
type TempTransactionalOperation struct {
	Operation string `json:"operation"`
	Request   any    `json:"request"`
}

type TempTransactionalUpsert struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

type TempTransactionalDelete struct {
	Key string `json:"key"`
}

var (
	actorLogs           = []actorLogEntry{}
	actorLogsMutex      = &sync.Mutex{}
	registeredActorType = getActorType()
	actors              sync.Map
)

var envOverride sync.Map

func getEnv(envName string) string {
	value, ok := envOverride.Load(envName)
	if ok {
		return fmt.Sprintf("%v", value)
	}

	return os.Getenv(envName)
}

func resetLogs() {
	actorLogsMutex.Lock()
	defer actorLogsMutex.Unlock()

	// Reset the slice without clearing the memory
	actorLogs = actorLogs[:0]
}

func getActorType() string {
	actorType := getEnv(actorTypeEnvName)
	if actorType == "" {
		return defaultActorType
	}

	return actorType
}

func getActorRemindersPartitions() int {
	val := getEnv(actorRemindersPartitionsEnvName)
	if val == "" {
		return 0
	}

	n, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}

	return n
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
	actorLogsMutex.Lock()
	defer actorLogsMutex.Unlock()

	dst := make([]actorLogEntry, len(actorLogs))
	copy(dst, actorLogs)
	return dst
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
	if r.Method == http.MethodDelete {
		resetLogs()
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	log.Print("Responding with logs:")
	json.NewEncoder(io.MultiWriter(w, os.Stdout)).
		Encode(getLogs())
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	daprConfigResponse := daprConfig{
		[]string{getActorType()},
		actorIdleTimeout,
		actorScanInterval,
		drainOngoingCallTimeout,
		drainRebalancedActors,
		getActorRemindersPartitions(),
	}

	log.Printf("Processing dapr request for %s, responding with %#v", r.URL.RequestURI(), daprConfigResponse)

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

	actorID := createActorID(actorType, id)
	log.Printf("storing, actorID is %s\n", actorID)

	actors.Store(actorID, daprActor{
		actorType: actorType,
		id:        actorID,
		value:     epoch(),
	})

	// if it's a state test, call state apis
	if method == "savestatetest" || method == "getstatetest" ||
		method == "savestatetest2" || method == "getstatetest2" {
		e := actorStateTest(method, w, actorType, id)
		if e != nil {
			return
		}
	}

	// Specific case to test reminder that deletes itself in its callback
	if id == "1001e" {
		url := fmt.Sprintf(actorReminderURLFormat, daprHTTPPort, actorType, id, "reminders", method)
		_, e := httpCall("DELETE", url, nil, 204)
		if e != nil {
			return
		}
	}

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
		fmt.Printf("Error: %v", err.Error()) //nolint:forbidigo
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

func deactivateActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s actor request for %s", r.Method, r.URL.RequestURI())

	start := epoch()

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]

	if actorType != registeredActorType {
		log.Printf("Unknown actor type: %s", actorType)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	actorID := createActorID(actorType, id)
	action := ""

	_, ok := actors.Load(actorID)
	if ok && r.Method == "DELETE" {
		action = "deactivation"
		actors.Delete(actorID)
	}

	appendLog(actorType, id, action, start)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

// calls Dapr's Actor method/timer/reminder: simulating actor client call.
func testCallActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s test request for %s", r.Method, r.URL.RequestURI())

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]
	callType := mux.Vars(r)["callType"]
	method := mux.Vars(r)["method"]

	url := fmt.Sprintf(actorMethodURLFormat, daprHTTPPort, actorType, id, callType, method)

	log.Printf("Invoking: %s %s\n", r.Method, url)
	expectedHTTPCode := 200
	var req timerReminderRequest
	switch callType {
	case "method":
		// NO OP
	case "timers":
		fallthrough
	case "reminders":
		if r.Method == http.MethodGet {
			expectedHTTPCode = 200
		} else {
			expectedHTTPCode = 204
		}
		body, err := io.ReadAll(r.Body)
		defer r.Body.Close()
		if err != nil {
			log.Printf("Could not get reminder request: %s", err.Error())
			return
		}

		log.Println("Body data: " + string(body))
		json.Unmarshal(body, &req)
	}

	body, err := httpCall(r.Method, url, req, expectedHTTPCode)
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
		log.Printf("Could not parse actor's test response: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Write(response.Data)
}

func testCallMetadataHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s test request for %s", r.Method, r.URL.RequestURI())

	metadataURL := fmt.Sprintf(daprBaseURLFormat+"/metadata", daprHTTPPort)
	body, err := httpCall(r.Method, metadataURL, nil, 200)
	if err != nil {
		log.Printf("Could not read metadata response: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

func shutdownHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s test request for %s", r.Method, r.URL.RequestURI())

	shutdownURL := fmt.Sprintf(daprBaseURLFormat+"/shutdown", daprHTTPPort)
	_, err := httpCall(r.Method, shutdownURL, nil, 204)
	if err != nil {
		log.Printf("Could not shutdown sidecar: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	go func() {
		time.Sleep(1 * time.Second)
		log.Fatal("simulating fatal shutdown")
	}()
}

func shutdownSidecarHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s test request for %s", r.Method, r.URL.RequestURI())

	shutdownURL := fmt.Sprintf(daprBaseURLFormat+"/shutdown", daprHTTPPort)
	_, err := httpCall(r.Method, shutdownURL, nil, 204)
	if err != nil {
		log.Printf("Could not shutdown sidecar: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func testEnvHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s test request for %s", r.Method, r.URL.RequestURI())

	envName := mux.Vars(r)["envName"]
	if r.Method == http.MethodGet {
		envValue := getEnv(envName)

		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(envValue))
	}

	if r.Method == http.MethodPost {
		body, err := io.ReadAll(r.Body)
		defer r.Body.Close()
		if err != nil {
			log.Printf("Could not read config env value: %s", err.Error())
			return
		}

		envOverride.Store(envName, string(body))
	}
}

// the test side calls the 4 cases below in order
func actorStateTest(testName string, w http.ResponseWriter, actorType string, id string) error {
	// save multiple key values
	if testName == "savestatetest" {
		url := fmt.Sprintf(actorSaveStateURLFormat, daprHTTPPort, actorType, id)

		operations := []TempTransactionalOperation{
			{
				Operation: "upsert",
				Request: TempTransactionalUpsert{
					Key:   "key1",
					Value: "data1",
				},
			},
			{
				Operation: "upsert",
				Request: TempTransactionalUpsert{
					Key:   "key2",
					Value: "data2",
				},
			},
			{
				Operation: "upsert",
				Request: TempTransactionalUpsert{
					Key:   "key3",
					Value: "data3",
				},
			},
			{
				Operation: "upsert",
				Request: TempTransactionalUpsert{
					Key:   "key4",
					Value: "data4",
				},
			},
		}

		_, err := httpCall("POST", url, operations, 201)
		if err != nil {
			log.Printf("actor state call failed: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return err
		}
	} else if testName == "getstatetest" {
		// perform a get on a key saved above
		url := fmt.Sprintf(actorGetStateURLFormat, daprHTTPPort, actorType, id, "key1")

		_, err := httpCall("GET", url, nil, 200)
		if err != nil {
			log.Printf("actor state call failed: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return err
		}

		// query a non-existing key.  This should return 204 with 0 length response.
		url = fmt.Sprintf(actorGetStateURLFormat, daprHTTPPort, actorType, id, "keynotpresent")
		body, err := httpCall("GET", url, nil, 204)
		if err != nil {
			log.Printf("actor state call failed: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return err
		}

		if len(body) != 0 {
			log.Println("expected 0 length response")
			w.WriteHeader(http.StatusInternalServerError)
			return errors.New("expected 0 length response")
		}

		// query a non-existing actor.  This should return 400.
		url = fmt.Sprintf(actorGetStateURLFormat, daprHTTPPort, actorType, "actoriddoesnotexist", "keynotpresent")
		_, err = httpCall("GET", url, nil, 400)
		if err != nil {
			log.Printf("actor state call failed: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return err
		}
	} else if testName == "savestatetest2" {
		// perform another transaction including a delete
		url := fmt.Sprintf(actorSaveStateURLFormat, daprHTTPPort, actorType, id)

		// modify 1 key and delete another
		operations := []TempTransactionalOperation{
			{
				Operation: "upsert",
				Request: TempTransactionalUpsert{
					Key:   "key1",
					Value: "data1v2",
				},
			},

			{
				Operation: "delete",
				Request: TempTransactionalDelete{
					Key: "key4",
				},
			},
		}

		_, err := httpCall("POST", url, operations, 201)
		if err != nil {
			log.Printf("actor state call failed: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return err
		}
	} else if testName == "getstatetest2" {
		// perform a get on an existing key
		url := fmt.Sprintf(actorGetStateURLFormat, daprHTTPPort, actorType, id, "key1")

		_, err := httpCall("GET", url, nil, 200)
		if err != nil {
			log.Printf("actor state call failed: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return err
		}

		// query a non-existing key - this was present but deleted.  This should return 204 with 0 length response.
		url = fmt.Sprintf(actorGetStateURLFormat, daprHTTPPort, actorType, id, "key4")

		body, err := httpCall("GET", url, nil, 204)
		if err != nil {
			log.Printf("actor state call failed: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return err
		}

		if len(body) != 0 {
			log.Println("expected 0 length response")
			w.WriteHeader(http.StatusInternalServerError)
			return errors.New("expected 0 length response")
		}
	} else {
		return errors.New("actorStateTest() - unexpected option")
	}

	return nil
}

func nonHostedTestHandler(w http.ResponseWriter, r *http.Request) {
	log.Print("Testing non-hosted actor reminders")

	url := fmt.Sprintf(actorReminderURLFormat, daprHTTPPort, "nonhosted", "id0", "reminders", "myreminder")
	tests := map[string]struct {
		Method string
		Body   any
	}{
		"GetReminder":    {"GET", nil},
		"CreateReminder": {"PUT", struct{}{}},
		"DeleteReminder": {"DELETE", struct{}{}},
	}
	for op, t := range tests {
		body, err := httpCall(t.Method, url, t.Body, http.StatusForbidden)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error performing %s request: %v", op, err), http.StatusInternalServerError)
			return
		}
		if !bytes.Contains(body, []byte("ERR_ACTOR_REMINDER_NON_HOSTED")) {
			http.Error(w, fmt.Sprintf("Response from %s doesn't contain the required error message: %s", op, string(body)), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "OK")
}

func httpCall(method string, url string, requestBody interface{}, expectedHTTPStatusCode int) ([]byte, error) {
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

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer res.Body.Close()

	if res.StatusCode != expectedHTTPStatusCode {
		var errBody []byte
		errBody, err = io.ReadAll(res.Body)
		if err == nil {
			return nil, fmt.Errorf("Expected http status %d, received %d, payload ='%s'", expectedHTTPStatusCode, res.StatusCode, string(errBody)) //nolint:stylecheck
		}

		return nil, fmt.Errorf("Expected http status %d, received %d", expectedHTTPStatusCode, res.StatusCode) //nolint:stylecheck
	}

	resBody, err := io.ReadAll(res.Body)
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
	return int(time.Now().UnixMilli())
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/dapr/config", configHandler).Methods("GET")

	// The POST method is used to register reminder
	// The DELETE method is used to unregister reminder
	// The PATCH method is used to rename reminder
	// The GET method is used to get reminder
	router.HandleFunc("/test/{actorType}/{id}/{callType}/{method}", testCallActorHandler).Methods("POST", "DELETE", "PATCH", "GET")

	router.HandleFunc("/actors/{actorType}/{id}/method/{method}", actorMethodHandler).Methods("PUT")
	router.HandleFunc("/actors/{actorType}/{id}/method/{reminderOrTimer}/{method}", actorMethodHandler).Methods("PUT")

	router.HandleFunc("/actors/{actorType}/{id}", deactivateActorHandler).Methods("POST", "DELETE")

	router.HandleFunc("/test/nonhosted", nonHostedTestHandler).Methods("POST")

	router.HandleFunc("/test/logs", logsHandler).Methods("GET", "DELETE")
	router.HandleFunc("/test/metadata", testCallMetadataHandler).Methods("GET")
	router.HandleFunc("/test/env/{envName}", testEnvHandler).Methods("GET", "POST")
	router.HandleFunc("/test/shutdown", shutdownHandler).Methods("POST")
	router.HandleFunc("/test/shutdownsidecar", shutdownSidecarHandler).Methods("POST")
	router.HandleFunc("/healthz", healthzHandler).Methods("GET")

	return router
}

func main() {
	log.Printf("Actor App - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
