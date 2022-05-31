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
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const appPort = 3000

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
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/test-topic", testTopicHandler).Methods("POST", "OPTIONS")
	router.HandleFunc("/custom-path", testRoutedTopicHandler).Methods("POST", "OPTIONS")
	router.HandleFunc("/tests/get_received_topics", testHandler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func startServer() {
	// Create a server capable of supporting HTTP2 Cleartext connections
	// Also supports HTTP1.1 and upgrades from HTTP1.1 to HTTP2
	h2s := &http2.Server{}
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", appPort),
		Handler: h2c.NewHandler(appRouter(), h2s),
	}

	// Stop the server when we get a termination signal
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		// Wait for cancelation signal
		<-stopCh
		log.Println("Shutdown signal received")
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	// Blocking call
	err := server.ListenAndServe()
	if err != http.ErrServerClosed {
		log.Fatalf("Failed to run server: %v", err)
	}
	log.Println("Server shut down")
}

func main() {
	log.Printf("Hello Dapr - listening on http://localhost:%d", appPort)
	startServer()
}
