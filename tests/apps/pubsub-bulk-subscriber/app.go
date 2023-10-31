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
	appPort                      = 3000
	pubsubRawSubTopic            = "pubsub-raw-sub-topic-http"
	pubsubCESubTopic             = "pubsub-ce-sub-topic-http"
	pubsubRawBulkSubTopic        = "pubsub-raw-bulk-sub-topic-http"
	pubsubCEBulkSubTopic         = "pubsub-ce-bulk-sub-topic-http"
	pubsubDeadBulkSubTopic       = "pubsub-dead-bulk-sub-topic-http"
	pubsubDeadLetterBulkSubTopic = "pubsub-dead-letter-bulk-sub-topic-http"
	PubSubEnvVar                 = "DAPR_TEST_PUBSUB_NAME"
	statusSuccess                = "SUCCESS"
	statusDrop                   = "DROP"
)

var pubsubkafkaName = "kafka-messagebus"

func init() {
	if psName := os.Getenv(PubSubEnvVar); len(psName) != 0 {
		pubsubkafkaName = psName
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
	ReceivedByTopicRawSub     []string `json:"pubsub-raw-sub-topic"`
	ReceivedByTopicCESub      []string `json:"pubsub-ce-sub-topic"`
	ReceivedByTopicRawBulkSub []string `json:"pubsub-raw-bulk-sub-topic"`
	ReceivedByTopicCEBulkSub  []string `json:"pubsub-ce-bulk-sub-topic"`
	ReceivedByTopicDead       []string `json:"pubsub-dead-bulk-topic"`
	ReceivedByTopicDeadLetter []string `json:"pubsub-deadletter-bulk-topic"`
}

type subscription struct {
	PubsubName      string            `json:"pubsubname"`
	Topic           string            `json:"topic"`
	Route           string            `json:"route"`
	DeadLetterTopic string            `json:"deadLetterTopic"`
	Metadata        map[string]string `json:"metadata"`
	BulkSubscribe   BulkSubscribe     `json:"bulkSubscribe"`
}

type BulkSubscribe struct {
	Enabled            bool  `json:"enabled"`
	MaxMessagesCount   int32 `json:"maxMessagesCount,omitempty"`
	MaxAwaitDurationMs int32 `json:"maxAwaitDurationMs,omitempty"`
}

type BulkRawMessage struct {
	Entries  []BulkMessageRawEntry `json:"entries"`
	Topic    string                `json:"topic"`
	Metadata map[string]string     `json:"metadata"`
}

type BulkMessageRawEntry struct {
	EntryId     string            `json:"entryId"` //nolint:stylecheck
	Event       string            `json:"event"`
	ContentType string            `json:"contentType,omitempty"`
	Metadata    map[string]string `json:"metadata"`
}

type BulkMessage struct {
	Entries  []BulkMessageEntry `json:"entries"`
	Topic    string             `json:"topic"`
	Metadata map[string]string  `json:"metadata"`
}

type BulkMessageEntry struct {
	EntryId     string                 `json:"entryId"` //nolint:stylecheck
	Event       map[string]interface{} `json:"event"`
	ContentType string                 `json:"contentType,omitempty"`
	Metadata    map[string]string      `json:"metadata"`
}

type AppBulkMessageEntry struct {
	EntryId     string            `json:"entryId"` //nolint:stylecheck
	EventStr    string            `json:"event"`
	ContentType string            `json:"contentType,omitempty"`
	Metadata    map[string]string `json:"metadata"`
}

type BulkSubscribeResponseEntry struct {
	EntryId string `json:"entryId"` //nolint:stylecheck
	Status  string `json:"status"`
}

// BulkSubscribeResponse is the whole bulk subscribe response sent by app
type BulkSubscribeResponse struct {
	Statuses []BulkSubscribeResponseEntry `json:"statuses"`
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
	// respond with success for all messages in bulk
	respondWithSuccessBulk
)

var (
	// using sets to make the test idempotent on multiple delivery of same message
	receivedMessagesSubRaw         sets.Set[string]
	receivedMessagesSubCE          sets.Set[string]
	receivedMessagesBulkRaw        sets.Set[string]
	receivedMessagesBulkCE         sets.Set[string]
	receivedMessagesBulkDead       sets.Set[string]
	receivedMessagesBulkDeadLetter sets.Set[string]
	desiredResponse                respondWith
	lock                           sync.Mutex
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
			PubsubName: pubsubkafkaName,
			Topic:      pubsubRawSubTopic,
			Route:      pubsubRawSubTopic,
			Metadata: map[string]string{
				"rawPayload": "true",
			},
		},
		{
			PubsubName: pubsubkafkaName,
			Topic:      pubsubCESubTopic,
			Route:      pubsubCESubTopic,
		},
		{
			PubsubName: pubsubkafkaName,
			Topic:      pubsubRawBulkSubTopic,
			Route:      pubsubRawBulkSubTopic,
			BulkSubscribe: BulkSubscribe{
				Enabled:            true,
				MaxMessagesCount:   60,
				MaxAwaitDurationMs: 1000,
			},
			Metadata: map[string]string{
				"rawPayload": "true",
			},
		},
		{
			PubsubName: pubsubkafkaName,
			Topic:      pubsubCEBulkSubTopic,
			Route:      pubsubCEBulkSubTopic,
			BulkSubscribe: BulkSubscribe{
				Enabled:            true,
				MaxMessagesCount:   60,
				MaxAwaitDurationMs: 1000,
			},
		},
		{
			PubsubName:      pubsubkafkaName,
			Topic:           pubsubDeadBulkSubTopic,
			Route:           pubsubDeadBulkSubTopic,
			DeadLetterTopic: pubsubDeadLetterBulkSubTopic,
		},
		{
			PubsubName: pubsubkafkaName,
			Topic:      pubsubDeadLetterBulkSubTopic,
			Route:      pubsubDeadLetterBulkSubTopic,
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
	if strings.HasSuffix(r.URL.String(), pubsubRawSubTopic) {
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

func readBulkMessageBody(reqID string, r *http.Request) (msgs []AppBulkMessageEntry, err error) {
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
		return nil, err
	}

	if strings.HasSuffix(r.URL.String(), pubsubRawBulkSubTopic) {
		msgs, err = extractBulkMessage(reqID, body, true)
		if err != nil {
			return nil, fmt.Errorf("error from extractBulkMessage: %w", err)
		}
	} else {
		msgs, err = extractBulkMessage(reqID, body, false)
		if err != nil {
			return nil, fmt.Errorf("error from extractBulkMessage: %w", err)
		}
	}
	return msgs, nil
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

	if err != nil {
		log.Printf("(%s) Responding with DROP due to error: %v", reqID, err)
		// Return 200 with DROP status to drop message
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
			Status:  statusDrop,
		})
		return
	}

	lock.Lock()
	defer lock.Unlock()
	if strings.HasSuffix(r.URL.String(), pubsubRawSubTopic) && !receivedMessagesSubRaw.Has(msg) {
		receivedMessagesSubRaw.Insert(msg)
	} else if strings.HasSuffix(r.URL.String(), pubsubCESubTopic) && !receivedMessagesSubCE.Has(msg) {
		receivedMessagesSubCE.Insert(msg)
	} else {
		// This case is triggered when there is multiple redelivery of same message or a message
		// is thre for an unknown URL path

		errorMessage := fmt.Sprintf("Unexpected/Multiple redelivery of message from %s", r.URL.String())
		log.Printf("(%s) Responding with DROP. %s", reqID, errorMessage)
		// Return success with DROP status to drop message
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(appResponse{
			Message: errorMessage,
			Status:  statusDrop,
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
			Status:  statusSuccess,
		})
	}
}

func bulkSubscribeHandler(w http.ResponseWriter, r *http.Request) {
	reqID, ok := r.Context().Value("reqid").(string)
	log.Printf("(%s) bulkSubscribeHandler called %s.", reqID, r.URL)
	if reqID == "" || !ok {
		reqID = uuid.New().String()
	}

	msgs, err := readBulkMessageBody(reqID, r)

	bulkResponseEntries := make([]BulkSubscribeResponseEntry, len(msgs))

	if err != nil {
		log.Printf("(%s) Responding with DROP due to error: %v", reqID, err)
		// Return 200 with DROP status to drop message
		w.WriteHeader(http.StatusOK)
		for i, msg := range msgs {
			entryResponse := BulkSubscribeResponseEntry{}
			entryResponse.EntryId = msg.EntryId
			entryResponse.Status = statusDrop
			bulkResponseEntries[i] = entryResponse
		}
		json.NewEncoder(w).Encode(BulkSubscribeResponse{
			Statuses: bulkResponseEntries,
		})
		return
	}

	// Success is the default status for this handler, can be overridden by the desiredResponse.
	// Supported values are "SUCCESS", "DROP" and "RETRY".
	returnStatus := statusSuccess
	if desiredResponse == respondWithDrop {
		returnStatus = statusDrop
	}

	for i, msg := range msgs {
		entryResponse := BulkSubscribeResponseEntry{}
		log.Printf("(%s) bulkSubscribeHandler called %s.Index: %d, Message: %s", reqID, r.URL, i, msg)
		log.Printf("(%s) Responding with %s for entryId %s", reqID, returnStatus, msg.EntryId)
		entryResponse.EntryId = msg.EntryId
		entryResponse.Status = returnStatus
		bulkResponseEntries[i] = entryResponse

		if strings.HasSuffix(r.URL.String(), pubsubRawBulkSubTopic) && !receivedMessagesBulkRaw.Has(msg.EventStr) {
			receivedMessagesBulkRaw.Insert(msg.EventStr)
		} else if strings.HasSuffix(r.URL.String(), pubsubCEBulkSubTopic) && !receivedMessagesBulkCE.Has(msg.EventStr) {
			receivedMessagesBulkCE.Insert(msg.EventStr)
		} else if strings.HasSuffix(r.URL.String(), pubsubDeadBulkSubTopic) && !receivedMessagesBulkDead.Has(msg.EventStr) {
			receivedMessagesBulkDead.Insert(msg.EventStr)
		} else if strings.HasSuffix(r.URL.String(), pubsubDeadLetterBulkSubTopic) && !receivedMessagesBulkDeadLetter.Has(msg.EventStr) {
			receivedMessagesBulkDeadLetter.Insert(msg.EventStr)
		} else {
			// This case is triggered when there is multiple redelivery of same message or a message
			// is thre for an unknown URL path

			errorMessage := fmt.Sprintf("Unexpected/Multiple redelivery of message during bulk subscribe from %s", r.URL.String())
			log.Printf("(%s) Responding with DROP during bulk subscribe. %s", reqID, errorMessage)
			entryResponse.Status = statusDrop
		}
	}

	w.WriteHeader(http.StatusOK)
	log.Printf("(%s) Responding with SUCCESS", reqID)
	json.NewEncoder(w).Encode(BulkSubscribeResponse{
		Statuses: bulkResponseEntries,
	})
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
	log.Printf("(%s) output='%s'", reqID, msg)

	return msg, nil
}

func extractBulkMessage(reqID string, body []byte, isRawPayload bool) ([]AppBulkMessageEntry, error) {
	log.Printf("(%s) extractBulkMessage() called with body=%s", reqID, string(body))

	if !isRawPayload {
		var bulkMsg BulkMessage
		err := json.Unmarshal(body, &bulkMsg)
		if err != nil {
			log.Printf("(%s) Could not unmarshal bulkMsg: %v", reqID, err)
			return nil, err
		}

		finalMsgs := make([]AppBulkMessageEntry, len(bulkMsg.Entries))
		for i, entry := range bulkMsg.Entries {
			entryCEData := entry.Event["data"].(string)
			appMsg := AppBulkMessageEntry{
				EntryId:  entry.EntryId,
				EventStr: entryCEData,
			}
			finalMsgs[i] = appMsg
			log.Printf("(%s) output at index: %d, entry id:'%s' is: '%s':", reqID, i, entry.EntryId, entryCEData)
		}
		return finalMsgs, nil
	}
	var bulkMsg BulkRawMessage
	err := json.Unmarshal(body, &bulkMsg)
	if err != nil {
		log.Printf("(%s) Could not unmarshal raw bulkMsg: %v", reqID, err)
		return nil, err
	}

	finalMsgs := make([]AppBulkMessageEntry, len(bulkMsg.Entries))
	for i, entry := range bulkMsg.Entries {
		entryData, err := base64.StdEncoding.DecodeString(entry.Event)
		if err != nil {
			log.Printf("(%s) Could not base64 decode in bulk entry: %v", reqID, err)
			continue
		}

		entryDataStr := string(entryData)
		log.Printf("(%s) output from base64 in bulk entry %s is:'%s'", reqID, entry.EntryId, entryDataStr)

		var actualMsg string
		err = json.Unmarshal([]byte(entryDataStr), &actualMsg)
		if err != nil {
			// Log only
			log.Printf("(%s) Error extracing JSON from raw event in bulk entry %s is: %v", reqID, entry.EntryId, err)
		} else {
			log.Printf("(%s) Output of JSON from raw event in bulk entry %s is: %v", reqID, entry.EntryId, actualMsg)
			entryDataStr = actualMsg
		}

		appMsg := AppBulkMessageEntry{
			EntryId:  entry.EntryId,
			EventStr: entryDataStr,
		}
		finalMsgs[i] = appMsg
		log.Printf("(%s) output at index: %d, entry id:'%s' is: '%s':", reqID, i, entry.EntryId, entryData)
	}
	return finalMsgs, nil
}

// the test calls this to get the messages received
func getReceivedMessages(w http.ResponseWriter, r *http.Request) {
	reqID, ok := r.Context().Value("reqid").(string)
	if reqID == "" || !ok {
		reqID = "s-" + uuid.New().String()
	}

	response := receivedMessagesResponse{
		ReceivedByTopicRawSub:     unique(sets.List(receivedMessagesSubRaw)),
		ReceivedByTopicCESub:      unique(sets.List(receivedMessagesSubCE)),
		ReceivedByTopicRawBulkSub: unique(sets.List(receivedMessagesBulkRaw)),
		ReceivedByTopicCEBulkSub:  unique(sets.List(receivedMessagesBulkCE)),
		ReceivedByTopicDead:       unique(sets.List(receivedMessagesBulkDead)),
		ReceivedByTopicDeadLetter: unique(sets.List(receivedMessagesBulkDeadLetter)),
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
	receivedMessagesSubRaw = sets.New[string]()
	receivedMessagesSubCE = sets.New[string]()
	receivedMessagesBulkRaw = sets.New[string]()
	receivedMessagesBulkCE = sets.New[string]()
	receivedMessagesBulkDead = sets.New[string]()
	receivedMessagesBulkDeadLetter = sets.New[string]()
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
	router.HandleFunc("/set-respond-success-bulk",
		setDesiredResponse(respondWithSuccessBulk, "set respond with success for bulk")).Methods("POST")
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

	router.HandleFunc("/"+pubsubRawSubTopic, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubCESubTopic, subscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubRawBulkSubTopic, bulkSubscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubCEBulkSubTopic, bulkSubscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubDeadBulkSubTopic, bulkSubscribeHandler).Methods("POST")
	router.HandleFunc("/"+pubsubDeadLetterBulkSubTopic, bulkSubscribeHandler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	// initialize sets on application start
	initializeSets()

	log.Printf("Dapr E2E test app: pubsub - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
