// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
)

const (
	appPort       = 3000
	pubsubHTTPAPI = "runtime-http-api"
	healthURL     = "http://localhost:3500/v1.0/healthz"
)

type topicsList struct {
	Topic []string
}

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

type daprAPIResponse struct {
	DaprHTTPSuccess int `json:"dapr_http_success"`
	DaprHTTPError   int `json:"dapr_http_error"`
	// TODO: gRPC API
}

var daprHTTPError, daprHTTPSuccess int

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("indexHandler is called\n")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// this handles /dapr/subscribe, which is called from dapr into this app.
// this returns the list of topics the app is subscribed to.
func configureSubscribeHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("configureSubscribeHandler called\n")

	var t topicsList
	t.Topic = append(t.Topic, pubsubHTTPAPI)
	log.Printf("configureSubscribeHandler subscribing to:%v\n", t.Topic)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(t.Topic)
}

// this handles messages published to "pubsub-http-server" and
// validates dapr's HTTP API is healthy.
func subscribeHTTPHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("subscribeHTTPHandler is called %s\n", r.URL)

	_, err := http.Get(healthURL)
	if err != nil {
		log.Printf("Error calling Dapr HTTP API")
		daprHTTPError++
		// Track the error but return success as we want to release the message
	} else {
		log.Printf("Success calling Dapr HTTP API")
		daprHTTPSuccess++
	}

	w.WriteHeader(http.StatusOK)
	defer r.Body.Close()

	json.NewEncoder(w).Encode(appResponse{
		Message: "success",
	})
}

func getDaprAPIResponse(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter getDaprAPIResponse")

	response := daprAPIResponse{
		DaprHTTPError:   daprHTTPError,
		DaprHTTPSuccess: daprHTTPSuccess,
	}

	log.Printf("DaprAPIResponse=%+v", response)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	log.Printf("Enter appRouter()")
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")

	router.HandleFunc("/tests/response", getDaprAPIResponse).Methods("GET")

	router.HandleFunc("/dapr/subscribe", configureSubscribeHandler).Methods("GET")

	router.HandleFunc("/"+pubsubHTTPAPI, subscribeHTTPHandler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr v2 - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
