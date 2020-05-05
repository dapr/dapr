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
	appPort         = 3000
	pubsubHTTPTopic = "runtime-pubsub-http"
	bindingsTopic   = "runtime-bindings-http"
	healthURL       = "http://localhost:3500/v1.0/healthz"
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

var pubsubDaprHTTPError, pubsubDaprHTTPSuccess, bindingsDaprHTTPError, bindingsDaprHTTPSuccess int

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
	t.Topic = append(t.Topic, pubsubHTTPTopic)
	log.Printf("configureSubscribeHandler subscribing to:%v\n", t.Topic)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(t.Topic)
}

// onPubsub handles messages published to "pubsub-http-server" and
// validates dapr's HTTP API is healthy.
func onPubsub(w http.ResponseWriter, r *http.Request) {
	log.Printf("onPubsub is called %s\n", r.URL)

	_, err := http.Get(healthURL)
	if err != nil {
		log.Printf("onPubsub(): Error calling Dapr HTTP API")
		pubsubDaprHTTPError++
		// Track the error but return success as we want to release the message
	} else {
		log.Printf("onPubsub(): Success calling Dapr HTTP API")
		pubsubDaprHTTPSuccess++
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{
		Message: "success",
	})
}

// onInputBinding handles incoming request from an input binding and
// validates dapr's HTTP API is healthy.
func onInputBinding(w http.ResponseWriter, r *http.Request) {
	log.Printf("onInputBinding is called %s\n", r.URL)

	if r.Method == http.MethodOptions {
		log.Printf("%s binding input has been accepted", bindingsTopic)
		// Sending StatusOK back to the topic, so it will not attempt to redeliver.
		w.WriteHeader(http.StatusOK)
		return
	}

	_, err := http.Get(healthURL)
	if err != nil {
		log.Printf("onInputBinding(): Error calling Dapr HTTP API")
		bindingsDaprHTTPError++
		// Track the error but return success as we want to release the message
	} else {
		log.Printf("onInputBinding(): Success calling Dapr HTTP API")
		bindingsDaprHTTPSuccess++
	}

	w.WriteHeader(http.StatusOK)
}

func getPubsubDaprAPIResponse(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter getDaprAPIResponse")

	response := daprAPIResponse{
		DaprHTTPError:   pubsubDaprHTTPError,
		DaprHTTPSuccess: pubsubDaprHTTPSuccess,
	}

	log.Printf("DaprAPIResponse=%+v", response)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func getBindingsDaprAPIResponse(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter getDaprAPIResponse")

	response := daprAPIResponse{
		DaprHTTPError:   bindingsDaprHTTPError,
		DaprHTTPSuccess: bindingsDaprHTTPSuccess,
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

	router.HandleFunc("/tests/pubsub", getPubsubDaprAPIResponse).Methods("GET")

	router.HandleFunc("/tests/bindings", getBindingsDaprAPIResponse).Methods("GET")

	router.HandleFunc("/dapr/subscribe", configureSubscribeHandler).Methods("GET")

	router.HandleFunc("/"+bindingsTopic, onInputBinding).Methods("POST", "OPTIONS")

	router.HandleFunc("/"+pubsubHTTPTopic, onPubsub).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr v2 - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
