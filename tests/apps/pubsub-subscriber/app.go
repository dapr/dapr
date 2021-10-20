// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	appPort   = 3000
	pubsubA   = "pubsub-a-topic-http"
	pubsubB   = "pubsub-b-topic-http"
	pubsubC   = "pubsub-c-topic-http"
	pubsubJob = "pubsub-job-topic-http"
	pubsubRaw = "pubsub-raw-topic-http"
)

type appResponse struct {
	// Status field for proper handling of errors form pubsub
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

type receivedMessagesResponse struct {
	ReceivedByTopicA   []string `json:"pubsub-a-topic"`
	ReceivedByTopicB   []string `json:"pubsub-b-topic"`
	ReceivedByTopicC   []string `json:"pubsub-c-topic"`
	ReceivedByTopicJob []string `json:"pubsub-job-topic"`
	ReceivedByTopicRaw []string `json:"pubsub-raw-topic"`
}

type subscription struct {
	PubsubName string            `json:"pubsubname"`
	Topic      string            `json:"topic"`
	Route      string            `json:"route"`
	Metadata   map[string]string `json:"metadata"`
}

// respondWith determines the response to return when a message
// is received.
type respondWith int

const (
	respondWithSuccess respondWith = iota
	// respond with empty json message
	respondWithEmptyJSON
	// respond with error
	respondWithError
	// respond with retry
	respondWithRetry
	// respond with invalid status
	respondWithInvalidStatus
)

var (
	// using sets to make the test idempotent on multiple delivery of same message
	receivedMessagesA   sets.String
	receivedMessagesB   sets.String
	receivedMessagesC   sets.String
	receivedMessagesJob sets.String
	receivedMessagesRaw sets.String
	desiredResponse     respondWith
	lock                sync.Mutex
)

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, _ *http.Request) {
	log.Printf("indexHandler is called\n")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// this handles /dapr/subscribe, which is called from dapr into this app.
// this returns the list of topics the app is subscribed to.
func configureSubscribeHandler(w http.ResponseWriter, _ *http.Request) {
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
		// pubsub-c-topic is loaded from the YAML/CRD
		// tests/config/app_topic_subscription_pubsub.yaml.
		{
			PubsubName: pubsubName,
			Topic:      pubsubJob,
			Route:      pubsubJob,
		},
		{
			PubsubName: pubsubName,
			Topic:      pubsubRaw,
			Route:      pubsubRaw,
			Metadata: map[string]string{
				"rawPayload": "true",
			},
		},
	}
	log.Printf("configureSubscribeHandler subscribing to:%v\n", t)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(t)
}

// this handles messages published to "pubsub-a-topic"
func subscribeHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("aHandler is called %s\n", r.URL)

	switch desiredResponse {
	case respondWithRetry:
		log.Printf("Responding with RETRY")
		// do not store received messages, respond with success but a retry status
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: "retry later",
			Status:  "RETRY",
		})

		return
	case respondWithError:
		log.Printf("Responding with ERROR")
		// do not store received messages, respond with error
		w.WriteHeader(http.StatusInternalServerError)

		return
	case respondWithInvalidStatus:
		log.Printf("Responding with INVALID")
		// do not store received messages, respond with success but an invalid status
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: "invalid status triggers retry",
			Status:  "INVALID",
		})

		return
	}
	defer r.Body.Close()

	var err error
	var data []byte
	var body []byte
	if r.Body != nil {
		if data, err = io.ReadAll(r.Body); err == nil {
			body = data
			log.Printf("assigned\n")
		}
	} else {
		// error
		err = errors.New("r.Body is nil")
	}

	if err != nil {
		log.Printf("Responding with DROP")
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
		log.Printf("Responding with DROP")
		// Return success with DROP status to drop message
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
			Status:  "DROP",
		})
		return
	}

	// Raw data does not have content-type, so it is handled as-is.
	// Because the publisher encodes to JSON before publishing, we need to decode here.
	if strings.HasSuffix(r.URL.String(), pubsubRaw) {
		var actualMsg string
		err = json.Unmarshal([]byte(msg), &actualMsg)
		if err != nil {
			log.Printf("Error extracing JSON from raw event: %v", err)
		} else {
			msg = actualMsg
		}
	}

	lock.Lock()
	defer lock.Unlock()
	if strings.HasSuffix(r.URL.String(), pubsubA) && !receivedMessagesA.Has(msg) {
		receivedMessagesA.Insert(msg)
	} else if strings.HasSuffix(r.URL.String(), pubsubB) && !receivedMessagesB.Has(msg) {
		receivedMessagesB.Insert(msg)
	} else if strings.HasSuffix(r.URL.String(), pubsubC) && !receivedMessagesC.Has(msg) {
		receivedMessagesC.Insert(msg)
	} else if strings.HasSuffix(r.URL.String(), pubsubJob) && !receivedMessagesJob.Has(msg) {
		receivedMessagesJob.Insert(msg)
	} else if strings.HasSuffix(r.URL.String(), pubsubRaw) && !receivedMessagesRaw.Has(msg) {
		receivedMessagesRaw.Insert(msg)
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
	if desiredResponse == respondWithEmptyJSON {
		log.Printf("Responding with {}")
		w.Write([]byte("{}"))
	} else {
		log.Printf("Responding with SUCCESS")
		json.NewEncoder(w).Encode(appResponse{
			Message: "consumed",
			Status:  "SUCCESS",
		})
	}
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

	if m["data_base64"] != nil {
		b, err := base64.StdEncoding.DecodeString(m["data_base64"].(string))
		if err != nil {
			log.Printf("Could not base64 decode, %s", err.Error())
			return "", err
		}

		msg := string(b)
		log.Printf("output='%s'\n", msg)
		return msg, nil
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
		ReceivedByTopicA:   unique(receivedMessagesA.List()),
		ReceivedByTopicB:   unique(receivedMessagesB.List()),
		ReceivedByTopicC:   unique(receivedMessagesC.List()),
		ReceivedByTopicJob: unique(receivedMessagesJob.List()),
		ReceivedByTopicRaw: unique(receivedMessagesRaw.List()),
	}

	log.Printf("receivedMessagesResponse=%s", response)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// setDesiredResponse returns an http.HandlerFunc that sets the desired response
// to `resp` and logs `msg`.
func setDesiredResponse(resp respondWith, msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		lock.Lock()
		defer lock.Unlock()
		log.Print(msg)
		desiredResponse = resp
		w.WriteHeader(http.StatusOK)
	}
}

// handler called for empty-json case.
func initializeHandler(w http.ResponseWriter, _ *http.Request) {
	initializeSets()
	w.WriteHeader(http.StatusOK)
}

// initialize all the sets for a clean test.
func initializeSets() {
	// initialize all the sets
	receivedMessagesA = sets.NewString()
	receivedMessagesB = sets.NewString()
	receivedMessagesC = sets.NewString()
	receivedMessagesJob = sets.NewString()
	receivedMessagesRaw = sets.NewString()
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	log.Printf("Enter appRouter()")
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")

	router.HandleFunc("/getMessages", getReceivedMessages).Methods("POST")
	router.HandleFunc("/set-respond-success",
		setDesiredResponse(respondWithSuccess, "set respond with success")).Methods("POST")
	router.HandleFunc("/set-respond-error",
		setDesiredResponse(respondWithError, "set respond with error")).Methods("POST")
	router.HandleFunc("/set-respond-retry",
		setDesiredResponse(respondWithRetry, "set respond with retry")).Methods("POST")
	router.HandleFunc("/set-respond-empty-json",
		setDesiredResponse(respondWithEmptyJSON, "set respond with empty json"))
	router.HandleFunc("/set-respond-invalid-status",
		setDesiredResponse(respondWithInvalidStatus, "set respond with invalid status")).Methods("POST")
	router.HandleFunc("/initialize", initializeHandler).Methods("POST")

	router.HandleFunc("/dapr/subscribe", configureSubscribeHandler).Methods("GET")

	router.HandleFunc("/"+pubsubA, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubB, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubC, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubJob, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubRaw, subscribeHandler).Methods("POST")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr v2 - listening on http://localhost:%d", appPort)

	// initialize sets on application start
	initializeSets()
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
