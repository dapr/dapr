// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
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
)

const (
	appPort = 3000
	pubsubA = "pubsub-a-topic"
	pubsubB = "pubsub-b-topic"
	pubsubC = "pubsub-c-topic"
)

type appResponse struct {
	// Status field for proper handling of errors form pubsub
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

type receivedMessagesResponse struct {
	ReceivedByTopicA []string `json:"pubsub-a-topic"`
	ReceivedByTopicB []string `json:"pubsub-b-topic"`
	ReceivedByTopicC []string `json:"pubsub-c-topic"`
}

type subscription struct {
	PubsubName string `json:"pubsubname"`
	Topic      string `json:"topic"`
	Route      string `json:"route"`
}

var (
	receivedMessagesA []string
	receivedMessagesB []string
	receivedMessagesC []string
	// boolean variable to respond with error if set
	respondWithError bool
	lock             sync.Mutex
)

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

	pubsubName := "messagebus"

	t := []subscription{
		{
			PubsubName: pubsubName,
			Topic:      pubsubA,
			Route:      pubsubA,
		},
		{
			PubsubName: pubsubName,
			Topic:      pubsubB,
			Route:      pubsubB,
		},
		{
			PubsubName: pubsubName,
			Topic:      pubsubC,
			Route:      pubsubC,
		},
	}
	log.Printf("configureSubscribeHandler subscribing to:%v\n", t)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(t)
}

// this handles messages published to "pubsub-a-topic"
func subscribeHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("aHandler is called %s\n", r.URL)

	if respondWithError {
		// do not store received messages, respond with error
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(appResponse{
			Message: "retry later",
			Status:  "RETRY",
		})
		return
	}
	w.WriteHeader(http.StatusOK)
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
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
			Status:  "DROP",
		})
		return
	}

	msg, err := extractMessage(body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
			Status:  "DROP",
		})
		return
	}

	lock.Lock()
	defer lock.Unlock()
	if strings.HasSuffix(r.URL.String(), pubsubA) {
		receivedMessagesA = append(receivedMessagesA, msg)
	} else if strings.HasSuffix(r.URL.String(), pubsubB) {
		receivedMessagesB = append(receivedMessagesB, msg)
	} else if strings.HasSuffix(r.URL.String(), pubsubC) {
		receivedMessagesC = append(receivedMessagesC, msg)
	} else {
		errorMessage := fmt.Sprintf("Unexpected message from %s", r.URL.String())
		log.Print(errorMessage)
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(appResponse{
			Message: errorMessage,
			Status:  "DROP",
		})
		return
	}

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

// the test calls this to get the messages received
func getReceivedMessages(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter getReceivedMessages")

	response := receivedMessagesResponse{
		ReceivedByTopicA: receivedMessagesA,
		ReceivedByTopicB: receivedMessagesB,
		ReceivedByTopicC: receivedMessagesC,
	}

	log.Printf("receivedMessagesResponse=%s", response)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// set to respond with error on receiving messages from pubsub
func setRespondWithError(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	lock.Lock()
	defer lock.Unlock()
	log.Print("set respond with error")
	respondWithError = true
	w.WriteHeader(http.StatusOK)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	log.Printf("Enter appRouter()")
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")

	router.HandleFunc("/tests/get", getReceivedMessages).Methods("POST")
	router.HandleFunc("/tests/set-respond-error", setRespondWithError).Methods("POST")

	router.HandleFunc("/dapr/subscribe", configureSubscribeHandler).Methods("GET")

	router.HandleFunc("/"+pubsubA, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubB, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubC, subscribeHandler).Methods("POST")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr v2 - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
