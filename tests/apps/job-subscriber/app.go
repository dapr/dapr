// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	appPort = 3000
	topic = "pubsub-job-topic"
)

type appResponse struct {
	// Status field for proper handling of errors form pubsub
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

type receivedMessagesResponse struct {
	Received []string `json:"received"`
}

type subscription struct {
	PubsubName string `json:"pubsubname"`
	Topic      string `json:"topic"`
	Route      string `json:"route"`
}

var (
	// using sets to make the test idempotent on multiple delivery of same message
	receivedMessages sets.String
	lock             sync.Mutex
)

// this handles /dapr/subscribe, which is called from dapr into this app.
// this returns the list of topics the app is subscribed to.
func configureSubscribeHandler(w http.ResponseWriter, _ *http.Request) {
	log.Printf("configureSubscribeHandler called\n")

	pubsubName := "messagebus"

	t := []subscription{
		{
			PubsubName: pubsubName,
			Topic:      topic,
			Route:      topic,
		},
	}
	log.Printf("configureSubscribeHandler subscribing to:%v\n", t)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(t)
}

// this handles messages published to "pubsub-job-topic"
func subscribeHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("subscribeHandler is called %s\n", r.URL)
	defer r.Body.Close()

	var err error
	var data []byte
	var body []byte
	if r.Body != nil {
		if data, err = ioutil.ReadAll(r.Body); err == nil {
			body = data
			log.Printf("assigned\n")
		}
	} else {
		// error
		err = errors.New("r.Body is nil")
	}

	if err != nil {
		// Return success with DROP status to drop message
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
			Status:  "DROP",
		})
		return
	}

	msg, err := extractMessage(body)
	if err != nil {
		// Return success with DROP status to drop message
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
			Status:  "DROP",
		})
		return
	}

	lock.Lock()
	defer lock.Unlock()
	if strings.HasSuffix(r.URL.String(), topic) && !receivedMessages.Has(msg) {
		receivedMessages.Insert(msg)
	} else {
		// This case is triggered when there is multiple redelivery of same message or a message
		// is thre for an unknown URL path

		errorMessage := fmt.Sprintf("Unexpected/Multiple redelivery of message from %s", r.URL.String())
		log.Print(errorMessage)
		// Return success with DROP status to drop message
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: errorMessage,
			Status:  "DROP",
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{
		Message: "consumed",
		Status:  "SUCCESS",
	})
}

func extractMessage(body []byte) (string, error) {
	log.Printf("extractMessage() called")

	log.Printf("body=%s", string(body))

	m := make(map[string]interface{})
	err := json.Unmarshal(body, &m)
	if err != nil {
		log.Printf("Could not unmarshal, %s", err.Error())
		return "", err
	}

	msg := m["data"].(string)
	log.Printf("output='%s'\n", msg)

	return msg, nil
}

func unique(slice []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range slice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

// the test calls this to get the messages received
func getReceivedMessages(w http.ResponseWriter, _ *http.Request) {
	log.Println("Enter getReceivedMessages")

	response := receivedMessagesResponse{
		Received: unique(receivedMessages.List()),
	}

	log.Printf("receivedMessagesResponse=%s", response)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handler called for empty-json case.
func initializeHandler(w http.ResponseWriter, _ *http.Request) {
	initializeSets()
	w.WriteHeader(http.StatusOK)
}

// initialize all the sets for a clean test.
func initializeSets() {
	// initialize all the sets
	receivedMessages = sets.NewString()
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	log.Printf("Enter appRouter()")
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/getMessages", getReceivedMessages).Methods("POST")
	router.HandleFunc("/initialize", initializeHandler).Methods("POST")

	router.HandleFunc("/dapr/subscribe", configureSubscribeHandler).Methods("GET")

	router.HandleFunc("/"+topic, subscribeHandler).Methods("POST")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr v2 - listening on http://localhost:%d", appPort)

	// initialize sets on application start
	initializeSets()
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
