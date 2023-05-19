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
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/mux"

	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort                       = 3000
	DaprTestTopicEnvVar           = "DAPR_TEST_TOPIC_NAME"
	DaprTestCustomPathRouteEnvVar = "DAPR_TEST_CUSTOM_PATH_ROUTE"
)

var (
	topicName       = "test-topic"
	topicCustomPath = "custom-path"
)

func init() {
	if envTopicName := os.Getenv(DaprTestTopicEnvVar); len(envTopicName) != 0 {
		topicName = envTopicName
	}

	if envCustomPath := os.Getenv(DaprTestCustomPathRouteEnvVar); len(envCustomPath) != 0 {
		topicCustomPath = envCustomPath
	}
}

type messageBuffer struct {
	lock            *sync.RWMutex
	successMessages []string
	routedMessages  []string
	// errorOnce is used to make sure that message is failed only once.
	errorOnce     bool
	failedMessage string
}

func (m *messageBuffer) addRouted(message string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.routedMessages = append(m.routedMessages, message)
}

func (m *messageBuffer) add(message string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.successMessages = append(m.successMessages, message)
}

func (m *messageBuffer) getAllRouted() []string {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.routedMessages
}

func (m *messageBuffer) getAllSuccessful() []string {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.successMessages
}

func (m *messageBuffer) getFailed() string {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.failedMessage
}

func (m *messageBuffer) fail(failedMessage string) bool {
	m.lock.Lock()
	defer m.lock.Unlock()
	// fail only for the first time. return false all other times.
	if !m.errorOnce {
		m.failedMessage = failedMessage
		m.errorOnce = true
		return m.errorOnce
	}
	return false
}

var messages = messageBuffer{
	lock:            &sync.RWMutex{},
	successMessages: []string{},
}

type indexHandlerResponse struct {
	Message string `json:"message,omitempty"`
}

type testHandlerResponse struct {
	ReceivedMessages []string `json:"received_messages,omitempty"`
	Message          string   `json:"message,omitempty"`
	FailedMessage    string   `json:"failed_message,omitempty"`
	RoutedMessages   []string `json:"routeed_messages,omitempty"`
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(indexHandlerResponse{Message: "OK"})
}

func testTopicHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("testTopicHandler called")
	if r.Method == http.MethodOptions {
		log.Println("test-topic binding input has been accepted")
		// Sending StatusOK back to the topic, so it will not attempt to redeliver on session restart.
		// Consumer marking successfully consumed offset.
		w.WriteHeader(http.StatusOK)
		return
	}

	var message string
	err := json.NewDecoder(r.Body).Decode(&message)
	log.Printf("Got message: %s", message)
	if err != nil {
		log.Printf("error parsing test-topic input binding payload: %s", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	if fail := messages.fail(message); fail {
		// simulate failure. fail only for the first time.
		log.Print("failing message")
		w.WriteHeader(http.StatusInternalServerError)

		return
	}
	messages.add(message)
	w.WriteHeader(http.StatusOK)
}

func testRoutedTopicHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("testRoutedTopicHandler called")
	if r.Method == http.MethodOptions {
		log.Println("test-topic routed binding input has been accepted")
		// Sending StatusOK back to the topic, so it will not attempt to redeliver on session restart.
		// Consumer marking successfully consumed offset.
		w.WriteHeader(http.StatusOK)
		return
	}

	var message string
	err := json.NewDecoder(r.Body).Decode(&message)
	log.Printf("Got message: %s", message)
	if err != nil {
		log.Printf("error parsing test-topic input binding payload: %s", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	messages.addRouted(message)
	w.WriteHeader(http.StatusOK)
}

func testHandler(w http.ResponseWriter, r *http.Request) {
	failedMessage := messages.getFailed()
	log.Printf("failed message %s", failedMessage)
	if err := json.NewEncoder(w).Encode(testHandlerResponse{
		ReceivedMessages: messages.getAllSuccessful(),
		FailedMessage:    failedMessage,
		RoutedMessages:   messages.getAllRouted(),
	}); err != nil {
		log.Printf("error encoding saved messages: %s", err)

		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(testHandlerResponse{
			Message: err.Error(),
		})
		return
	}
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc(fmt.Sprintf("/%s", topicName), testTopicHandler).Methods("POST", "OPTIONS")
	router.HandleFunc(fmt.Sprintf("/%s", topicCustomPath), testRoutedTopicHandler).Methods("POST", "OPTIONS")
	router.HandleFunc("/tests/get_received_topics", testHandler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
