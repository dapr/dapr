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
	"sync"

	"github.com/gorilla/mux"
)

const appPort = 3000

type messageBuffer struct {
	lock     *sync.RWMutex
	messages []string
}

func (m *messageBuffer) add(message string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.messages = append(m.messages, message)
}

func (m *messageBuffer) getAll() []string {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.messages
}

var messages messageBuffer = messageBuffer{
	lock:     &sync.RWMutex{},
	messages: []string{},
}

type indexHandlerResponse struct {
	Message string `json:"message,omitempty"`
}

type testHandlerResponse struct {
	ReceivedMessages []string `json:"received_messages,omitempty"`
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(indexHandlerResponse{Message: "OK"})
}

func testTopicHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		log.Println("test-topic binding input has been accepted")
		w.WriteHeader(http.StatusOK)
		return
	}

	var message string
	err := json.NewDecoder(r.Body).Decode(&message)
	if err != nil {
		log.Printf("error parsing test-topic input binding payload: %s", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	messages.add(message)
	w.WriteHeader(http.StatusOK)
}

func testHandler(w http.ResponseWriter, r *http.Request) {
	if err := json.NewEncoder(w).Encode(testHandlerResponse{ReceivedMessages: messages.getAll()}); err != nil {
		log.Printf("error encoding saved messages: %s", err)

		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error: " + err.Error()))
	}
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/test-topic", testTopicHandler).Methods("POST", "OPTIONS")
	router.HandleFunc("/tests/get_received_topics", testHandler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
