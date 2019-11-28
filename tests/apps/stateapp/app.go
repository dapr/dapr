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
	"time"
	"strings"

	"github.com/gorilla/mux"
)

const appPort = 3000
const stateURL = "http://localhost:3500/v1.0/state"

// appState represents a state in this app.
type appState struct {
	Data   string `json:"data,omitempty"`
}

// daprState represents a state in Dapr.
type daprState struct {
	Key    string   `json:"key,omitempty"`
	Value  *appState `json:"value,omitempty"`
}

// requestResponse represents a request or response for the APIs in this app.
type requestResponse struct {
	StartTime int           `json:"start_time,omitempty"`
	EndTime   int           `json:"end_time,omitempty"`
	States    []daprState   `json:"states,omitempty"`
	Message   string        `json:"message,omitempty"`
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
}

func save(states []daprState) error {
	log.Printf("Processing save request for %d entries.", len(states))

	jsonValue, err := json.Marshal(states)
	if err != nil {
		log.Print("Could save states in Dapr: %s", err.Error())
		return err
	}

	log.Printf("Posting state to %s with '%s'", stateURL, jsonValue)
	_, err = http.Post(stateURL, "application/json", bytes.NewBuffer(jsonValue))

	return err
}

func get(key string) (*appState, error) {
	log.Printf("Processing get request for %s.", key)
	url := strings.Join([]string { stateURL, key }, "/")
	log.Printf("Fetching state from %s", url)
	res, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("Could not get value for key %s from Dapr: %s", key, err.Error())
	}

	body, err := ioutil.ReadAll(res.Body)
    if err != nil {
        return nil, fmt.Errorf("Could not load value for key %s from Dapr: %s", key, err.Error())
	}
	
	log.Printf("Found state for key %s: %s", key, body)

	var state = new(appState)
	if len(body) == 0 {
		return nil, nil
	}

	// a key not found in Dapr will return 200 but an empty response.
	err = json.Unmarshal([]byte(body), &state)
	if err != nil {
		return nil, fmt.Errorf("Could not parse value for key %s from Dapr: %s", key, err.Error())
	}

	return state, nil
}

func getAll(states []daprState) ([]daprState, error) {
	log.Printf("Processing get request for %d states.", len(states))

	var output []daprState
	for _, state := range states {
		value, err := get(state.Key)

		if err != nil {
			return nil, err
		}

		log.Printf("Result for get request for key %s: %v", state.Key, value)
		output = append(output, daprState {
			Key: state.Key,
			Value: value,
		})
	}

	log.Printf("Result for get request for %d states: %v", len(states), output)
	return output, nil
}

func delete(key string) error {
	log.Printf("Processing delete request for %s.", key)
	url := strings.Join([]string { stateURL, key }, "/")
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("Could not create delete request for key %s in Dapr: %s", key, err.Error())
	}

	log.Printf("Deleting state for %s", url)
	client := &http.Client{}
	_, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("Could not delete key %s in Dapr: %s", key, err.Error())
	}

	return nil
}

func deleteAll(states []daprState) error {
	log.Printf("Processing delete request for %d states.", len(states))

	for _, state := range states {
		err := delete(state.Key)

		if err != nil {
			return err
		}
	}

	return nil
}

// handles all APIs
func handler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing request for %s", r.URL.RequestURI())

	// Retrieve request body contents
	var req requestResponse
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		log.Printf("Could not parse request body: %s", err.Error())
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(requestResponse {
			Message: err.Error(),
		})
		return
	}

    var res = requestResponse {}
	var uri = r.URL.RequestURI()
	var states []daprState
	var statusCode = http.StatusOK

	res.StartTime = epoch()
	
	switch uri {
	case "/test/save":
		err = save(req.States)
	case "/test/get":
		states, err = getAll(req.States)
		res.States = states
	case "/test/delete":
		err = deleteAll(req.States)
	default:
		err = fmt.Errorf("Invalid URI: %s", uri)
		statusCode = http.StatusBadRequest
		res.Message = err.Error()
	}

	if err != nil && statusCode == http.StatusOK {
		statusCode = http.StatusInternalServerError
		res.Message = err.Error()
	}

	res.EndTime = epoch()

	if statusCode != http.StatusOK {
		log.Printf("Error status code %v: %v", statusCode, res.Message)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(res)
}

// epoch returns the current unix epoch timestamp
func epoch() int {
	return (int)(time.Now().UnixNano() / 1000000)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/test/save", handler).Methods("POST")
	router.HandleFunc("/test/get", handler).Methods("POST")
	router.HandleFunc("/test/delete", handler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("State App - listening on http://localhost:%d", appPort)
	log.Printf("State endpoint - to be saved at %s", stateURL)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
