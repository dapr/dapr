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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort            = 3000
	pubsubA            = "pubsub-a-topic-http"
	pubsubB            = "pubsub-b-topic-http"
	pubsubC            = "pubsub-c-topic-http"
	pubsubJob          = "pubsub-job-topic-http"
	pubsubRaw          = "pubsub-raw-topic-http"
	pubsubDead         = "pubsub-dead-topic-http"
	pubsubDeadLetter   = "pubsub-deadletter-topic-http"
	pubsubBulkTopic    = "pubsub-bulk-topic-http"
	pubsubRawBulkTopic = "pubsub-raw-bulk-topic-http"
	pubsubCEBulkTopic  = "pubsub-ce-bulk-topic-http"
	pubsubDefBulkTopic = "pubsub-def-bulk-topic-http"
	PubSubEnvVar       = "DAPR_TEST_PUBSUB_NAME"
)

var (
	pubsubName  = "messagebus"
	pubsubKafka = "kafka-messagebus"
)

func init() {
	if psName := os.Getenv(PubSubEnvVar); len(psName) != 0 {
		pubsubName = psName
	}
}

type appResponse struct {
	// Status field for proper handling of errors form pubsub
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

type receivedMessagesResponse struct {
	ReceivedByTopicA          []string `json:"pubsub-a-topic"`
	ReceivedByTopicB          []string `json:"pubsub-b-topic"`
	ReceivedByTopicC          []string `json:"pubsub-c-topic"`
	ReceivedByTopicJob        []string `json:"pubsub-job-topic"`
	ReceivedByTopicRaw        []string `json:"pubsub-raw-topic"`
	ReceivedByTopicDead       []string `json:"pubsub-dead-topic"`
	ReceivedByTopicDeadLetter []string `json:"pubsub-deadletter-topic"`
	ReceivedByTopicBulk       []string `json:"pubsub-bulk-topic"`
	ReceivedByTopicRawBulk    []string `json:"pubsub-raw-bulk-topic"`
	ReceivedByTopicCEBulk     []string `json:"pubsub-ce-bulk-topic"`
	ReceivedByTopicDefBulk    []string `json:"pubsub-def-bulk-topic"`
}

type subscription struct {
	PubsubName      string            `json:"pubsubname"`
	Topic           string            `json:"topic"`
	Route           string            `json:"route"`
	DeadLetterTopic string            `json:"deadLetterTopic"`
	Metadata        map[string]string `json:"metadata"`
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
	// respond with drop
	respondWithDrop
	// respond with invalid status
	respondWithInvalidStatus
)

var (
	// using sets to make the test idempotent on multiple delivery of same message
	receivedMessagesA            sets.Set[string]
	receivedMessagesB            sets.Set[string]
	receivedMessagesC            sets.Set[string]
	receivedMessagesJob          sets.Set[string]
	receivedMessagesRaw          sets.Set[string]
	receivedMessagesDead         sets.Set[string]
	receivedMessagesDeadLetter   sets.Set[string]
	receivedMessagesBulkTopic    sets.Set[string]
	receivedMessagesRawBulkTopic sets.Set[string]
	receivedMessagesCEBulkTopic  sets.Set[string]
	receivedMessagesDefBulkTopic sets.Set[string]
	desiredResponse              respondWith
	lock                         sync.Mutex
)

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, _ *http.Request) {
	log.Printf("indexHandler called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// this handles /dapr/subscribe, which is called from dapr into this app.
// this returns the list of topics the app is subscribed to.
func configureSubscribeHandler(w http.ResponseWriter, _ *http.Request) {
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
		{
			PubsubName:      pubsubName,
			Topic:           pubsubDead,
			Route:           pubsubDead,
			DeadLetterTopic: pubsubDeadLetter,
		},
		{
			PubsubName: pubsubName,
			Topic:      pubsubDeadLetter,
			Route:      pubsubDeadLetter,
		},
		{
			// receive normal string message from kafka pubsub
			PubsubName: pubsubKafka,
			Topic:      pubsubBulkTopic,
			Route:      pubsubBulkTopic,
		},
		{
			// receive raw payload message from kafka pubsub
			PubsubName: pubsubKafka,
			Topic:      pubsubRawBulkTopic,
			Route:      pubsubRawBulkTopic,
			Metadata: map[string]string{
				"rawPayload": "true",
			},
		},
		{
			// receive CE payload message from kafka pubsub
			PubsubName: pubsubKafka,
			Topic:      pubsubCEBulkTopic,
			Route:      pubsubCEBulkTopic,
		},
		{
			// receive def bulk payload message from redis pubsub (default)
			PubsubName: pubsubName,
			Topic:      pubsubDefBulkTopic,
			Route:      pubsubDefBulkTopic,
		},
	}

	log.Printf("configureSubscribeHandler called; subscribing to: %v\n", t)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(t)
}

func readMessageBody(reqID string, r *http.Request) (msg string, err error) {
	defer r.Body.Close()

	var body []byte
	if r.Body != nil {
		var data []byte
		data, err = io.ReadAll(r.Body)
		if err == nil {
			body = data
		}
	} else {
		// error
		err = errors.New("r.Body is nil")
	}

	if err != nil {
		return "", err
	}

	msg, err = extractMessage(reqID, body)
	if err != nil {
		return "", fmt.Errorf("error from extractMessage: %w", err)
	}

	// Raw data does not have content-type, so it is handled as-is.
	// Because the publisher encodes to JSON before publishing, we need to decode here.
	if strings.HasSuffix(r.URL.String(), pubsubRaw) || strings.HasSuffix(r.URL.String(), pubsubRawBulkTopic) {
		var actualMsg string
		err = json.Unmarshal([]byte(msg), &actualMsg)
		if err != nil {
			// Log only
			log.Printf("(%s) Error extracing JSON from raw event: %v", reqID, err)
		} else {
			msg = actualMsg
		}
	}

	return msg, nil
}

func subscribeHandler(w http.ResponseWriter, r *http.Request) {
	reqID, ok := r.Context().Value("reqid").(string)
	if reqID == "" || !ok {
		reqID = uuid.New().String()
	}

	msg, err := readMessageBody(reqID, r)

	// Before we handle the error, see if we need to respond in another way
	// We still want the message so we can log it
	log.Printf("(%s) subscribeHandler called %s. Message: %s", reqID, r.URL, msg)
	switch desiredResponse {
	case respondWithRetry:
		log.Printf("(%s) Responding with RETRY", reqID)
		// do not store received messages, respond with success but a retry status
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: "retry later",
			Status:  "RETRY",
		})
		return
	case respondWithError:
		log.Printf("(%s) Responding with ERROR", reqID)
		// do not store received messages, respond with error
		w.WriteHeader(http.StatusInternalServerError)
		return
	case respondWithInvalidStatus:
		log.Printf("(%s) Responding with INVALID", reqID)
		// do not store received messages, respond with success but an invalid status
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: "invalid status triggers retry",
			Status:  "INVALID",
		})
		return
	}

	if err != nil {
		log.Printf("(%s) Responding with DROP due to error: %v", reqID, err)
		// Return 200 with DROP status to drop message
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
			Status:  "DROP",
		})
		return
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
	} else if strings.HasSuffix(r.URL.String(), pubsubDead) && !receivedMessagesDead.Has(msg) {
		receivedMessagesDead.Insert(msg)
	} else if strings.HasSuffix(r.URL.String(), pubsubDeadLetter) && !receivedMessagesDeadLetter.Has(msg) {
		receivedMessagesDeadLetter.Insert(msg)
	} else if strings.HasSuffix(r.URL.String(), pubsubBulkTopic) && !receivedMessagesBulkTopic.Has(msg) {
		receivedMessagesBulkTopic.Insert(msg)
	} else if strings.HasSuffix(r.URL.String(), pubsubRawBulkTopic) && !receivedMessagesRawBulkTopic.Has(msg) {
		receivedMessagesRawBulkTopic.Insert(msg)
	} else if strings.HasSuffix(r.URL.String(), pubsubCEBulkTopic) && !receivedMessagesCEBulkTopic.Has(msg) {
		receivedMessagesCEBulkTopic.Insert(msg)
	} else if strings.HasSuffix(r.URL.String(), pubsubDefBulkTopic) && !receivedMessagesDefBulkTopic.Has(msg) {
		receivedMessagesDefBulkTopic.Insert(msg)
	} else {
		// This case is triggered when there is multiple redelivery of same message or a message
		// is thre for an unknown URL path

		errorMessage := fmt.Sprintf("Unexpected/Multiple redelivery of message from %s", r.URL.String())
		log.Printf("(%s) Responding with DROP. %s", reqID, errorMessage)
		// Return success with DROP status to drop message
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: errorMessage,
			Status:  "DROP",
		})
		return
	}

	// notice that dropped messages are also counted into received messages set
	if desiredResponse == respondWithDrop {
		log.Printf("(%s) Responding with DROP", reqID)
		// Return success with DROP status to drop message
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: "consumed",
			Status:  "DROP",
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	if desiredResponse == respondWithEmptyJSON {
		log.Printf("(%s) Responding with {}", reqID)
		w.Write([]byte("{}"))
	} else {
		log.Printf("(%s) Responding with SUCCESS", reqID)
		json.NewEncoder(w).Encode(appResponse{
			Message: "consumed",
			Status:  "SUCCESS",
		})
	}
}

func extractMessage(reqID string, body []byte) (string, error) {
	log.Printf("(%s) extractMessage() called with body=%s", reqID, string(body))

	m := make(map[string]interface{})
	err := json.Unmarshal(body, &m)
	if err != nil {
		log.Printf("(%s) Could not unmarshal: %v", reqID, err)
		return "", err
	}

	if m["data_base64"] != nil {
		b, err := base64.StdEncoding.DecodeString(m["data_base64"].(string))
		if err != nil {
			log.Printf("(%s) Could not base64 decode: %v", reqID, err)
			return "", err
		}

		msg := string(b)
		log.Printf("(%s) output from base64='%s'", reqID, msg)
		return msg, nil
	}

	msg := m["data"].(string)
	pubsubName := m["pubsubname"].(string)
	log.Printf("(%s) pubsub='%s' output='%s'", reqID, pubsubName, msg)

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
func getReceivedMessages(w http.ResponseWriter, r *http.Request) {
	reqID, ok := r.Context().Value("reqid").(string)
	if reqID == "" || !ok {
		reqID = "s-" + uuid.New().String()
	}

	response := receivedMessagesResponse{
		ReceivedByTopicA:          unique(sets.List(receivedMessagesA)),
		ReceivedByTopicB:          unique(sets.List(receivedMessagesB)),
		ReceivedByTopicC:          unique(sets.List(receivedMessagesC)),
		ReceivedByTopicJob:        unique(sets.List(receivedMessagesJob)),
		ReceivedByTopicRaw:        unique(sets.List(receivedMessagesRaw)),
		ReceivedByTopicDead:       unique(sets.List(receivedMessagesDead)),
		ReceivedByTopicDeadLetter: unique(sets.List(receivedMessagesDeadLetter)),
		ReceivedByTopicBulk:       unique(sets.List(receivedMessagesBulkTopic)),
		ReceivedByTopicRawBulk:    unique(sets.List(receivedMessagesRawBulkTopic)),
		ReceivedByTopicCEBulk:     unique(sets.List(receivedMessagesCEBulkTopic)),
		ReceivedByTopicDefBulk:    unique(sets.List(receivedMessagesDefBulkTopic)),
	}

	log.Printf("getReceivedMessages called. reqID=%s response=%s", reqID, response)

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
	receivedMessagesA = sets.New[string]()
	receivedMessagesB = sets.New[string]()
	receivedMessagesC = sets.New[string]()
	receivedMessagesJob = sets.New[string]()
	receivedMessagesRaw = sets.New[string]()
	receivedMessagesDead = sets.New[string]()
	receivedMessagesDeadLetter = sets.New[string]()
	receivedMessagesBulkTopic = sets.New[string]()
	receivedMessagesRawBulkTopic = sets.New[string]()
	receivedMessagesCEBulkTopic = sets.New[string]()
	receivedMessagesDefBulkTopic = sets.New[string]()
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	log.Printf("Called appRouter()")
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")

	router.HandleFunc("/getMessages", getReceivedMessages).Methods("POST")
	router.HandleFunc("/set-respond-success",
		setDesiredResponse(respondWithSuccess, "set respond with success")).Methods("POST")
	router.HandleFunc("/set-respond-error",
		setDesiredResponse(respondWithError, "set respond with error")).Methods("POST")
	router.HandleFunc("/set-respond-retry",
		setDesiredResponse(respondWithRetry, "set respond with retry")).Methods("POST")
	router.HandleFunc("/set-respond-drop",
		setDesiredResponse(respondWithDrop, "set respond with drop")).Methods("POST")
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
	router.HandleFunc("/"+pubsubDead, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubDeadLetter, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubBulkTopic, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubRawBulkTopic, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubCEBulkTopic, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubDefBulkTopic, subscribeHandler).Methods("POST")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	// initialize sets on application start
	initializeSets()

	log.Printf("Dapr E2E test app: pubsub - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
