// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

const (
	appPort    = 3000
	daprPort   = 3500
	pubsubName = "messagebus"
)

type publishCommand struct {
	Topic string `json:"topic"`
	Data  string `json:"data"`
}

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("indexHandler is called\n")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// when called by the test, this function publishes to dapr
// nolint:gosec
func performPublish(w http.ResponseWriter, r *http.Request) {
	log.Println("performPublish() called")

	var commandBody publishCommand
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	log.Printf("    commandBody.Topic=%s", commandBody.Topic)
	log.Printf("    commandBody.Data=%s", commandBody.Data)

	// based on commandBody.Topic, send to the appropriate topic
	resp := appResponse{Message: fmt.Sprintf("%s is not supported", commandBody.Topic)}

	startTime := epoch()

	jsonValue, err := json.Marshal(commandBody.Data)
	if err != nil {
		log.Printf("Error Marshaling")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	// publish to dapr
	url := fmt.Sprintf("http://localhost:%d/v1.0/publish/%s/%s", daprPort, pubsubName, commandBody.Topic)
	log.Printf("Publishing using url %s and body '%s'", url, jsonValue)

	postResp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonValue))
	if err != nil {
		if postResp != nil {
			log.Printf("Publish failed with error=%s, StatusCode=%d", err.Error(), postResp.StatusCode)
		} else {
			log.Printf("Publish failed with error=%s, response is nil", err.Error())
		}

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}
	defer postResp.Body.Close()

	// pass on status code to the calling application
	w.WriteHeader(postResp.StatusCode)

	if postResp.StatusCode == http.StatusOK {
		log.Printf("Publish succeeded")
		resp = appResponse{Message: "Success"}
	} else {
		log.Printf("Publish failed")
		resp = appResponse{Message: "Failed"}
	}
	resp.StartTime = startTime
	resp.EndTime = epoch()

	json.NewEncoder(w).Encode(resp)
}

// epoch returns the current unix epoch timestamp
func epoch() int {
	return (int)(time.Now().UTC().UnixNano() / 1000000)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	log.Printf("Enter appRouter()")
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/tests/publish", performPublish).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr v2 - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
