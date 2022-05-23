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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	appPort     = 3000
	pubsubName  = "messagebus"
	pubsubTopic = "pubsub-routing-http"

	pathA = "myevent.A"
	pathB = "myevent.B"
	pathC = "myevent.C"
	pathD = "myevent.D"
	pathE = "myevent.E"
	pathF = "myevent.F"
)

type appResponse struct {
	// Status field for proper handling of errors form pubsub
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

type routedMessagesResponse struct {
	RouteA []string `json:"route-a"`
	RouteB []string `json:"route-b"`
	RouteC []string `json:"route-c"`
	RouteD []string `json:"route-d"`
	RouteE []string `json:"route-e"`
	RouteF []string `json:"route-f"`
}

type subscription struct {
	PubsubName string            `json:"pubsubname"`
	Topic      string            `json:"topic"`
	Metadata   map[string]string `json:"metadata"`
	Routes     routes            `json:"routes,omitempty"`
}

type routes struct {
	Rules   []rule `json:"rules,omitempty"`
	Default string `json:"default,omitempty"`
}

type rule struct {
	Match string `json:"match"`
	Path  string `json:"path"`
}

var (
	// using sets to make the test idempotent on multiple delivery of same message
	routedMessagesA sets.String
	routedMessagesB sets.String
	routedMessagesC sets.String
	routedMessagesD sets.String
	routedMessagesE sets.String
	routedMessagesF sets.String
	lock            sync.Mutex
)

// initialize all the sets for a clean test.
func initializeSets() {
	lock.Lock()
	defer lock.Unlock()

	// initialize all the sets
	routedMessagesA = sets.NewString()
	routedMessagesB = sets.NewString()
	routedMessagesC = sets.NewString()
	routedMessagesD = sets.NewString()
	routedMessagesE = sets.NewString()
	routedMessagesF = sets.NewString()
}

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
			Topic:      pubsubTopic,
			Routes: routes{
				Rules: []rule{
					{
						Match: `event.type == "myevent.C"`,
						Path:  pathC,
					},
					{
						Match: `event.type == "myevent.B"`,
						Path:  pathB,
					},
				},
				Default: pathA,
			},
		},
	}
	log.Printf("configureSubscribeHandler called; subscribing to: %v\n", t)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(t)
}

func eventHandlerA(w http.ResponseWriter, r *http.Request) {
	eventHandler(w, r, routedMessagesA)
}

func eventHandlerB(w http.ResponseWriter, r *http.Request) {
	eventHandler(w, r, routedMessagesB)
}

func eventHandlerC(w http.ResponseWriter, r *http.Request) {
	eventHandler(w, r, routedMessagesC)
}

func eventHandlerD(w http.ResponseWriter, r *http.Request) {
	eventHandler(w, r, routedMessagesD)
}

func eventHandlerE(w http.ResponseWriter, r *http.Request) {
	eventHandler(w, r, routedMessagesE)
}

func eventHandlerF(w http.ResponseWriter, r *http.Request) {
	eventHandler(w, r, routedMessagesF)
}

func eventHandler(w http.ResponseWriter, r *http.Request, set sets.String) {
	reqID := uuid.New().String()
	log.Printf("(%s) eventHandler called %s", reqID, r.URL)

	var err error
	var body []byte
	if r.Body != nil {
		var data []byte
		data, err = io.ReadAll(r.Body)
		if err == nil {
			body = data
		}
	} else {
		log.Printf("(%s) r.Body is nil", reqID)
	}

	msg, err := extractMessage(reqID, body)
	if err != nil {
		log.Printf("(%s) Responding with DROP. Error from extractMessage: %v", reqID, err)
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
	set.Insert(msg)

	w.WriteHeader(http.StatusOK)
	log.Printf("(%s) Responding with SUCCESS", reqID)
	json.NewEncoder(w).Encode(appResponse{
		Message: "consumed",
		Status:  "SUCCESS",
	})
}

func extractMessage(reqID string, body []byte) (string, error) {
	log.Printf("(%s) extractMessage() called with body=%s", reqID, string(body))
	if body == nil {
		return "", errors.New("no body")
	}

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

// handler called for empty-json case.
func initializeHandler(w http.ResponseWriter, _ *http.Request) {
	initializeSets()
	w.WriteHeader(http.StatusOK)
}

// the test calls this to get the messages received
func getReceivedMessages(w http.ResponseWriter, r *http.Request) {
	reqID := r.URL.Query().Get("reqid")
	if reqID == "" {
		reqID = "s-" + uuid.New().String()
	}

	response := routedMessagesResponse{
		RouteA: unique(routedMessagesA.List()),
		RouteB: unique(routedMessagesB.List()),
		RouteC: unique(routedMessagesC.List()),
		RouteD: unique(routedMessagesD.List()),
		RouteE: unique(routedMessagesE.List()),
		RouteF: unique(routedMessagesF.List()),
	}

	log.Printf("getReceivedMessages called. reqID=%s response=%s", reqID, response)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func unique(slice []string) []string {
	keys := make(map[string]struct{})
	list := []string{}
	for _, entry := range slice {
		if _, exists := keys[entry]; !exists {
			keys[entry] = struct{}{}
			list = append(list, entry)
		}
	}
	return list
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	log.Printf("Called appRouter()")
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/getMessages", getReceivedMessages).Methods("POST")
	router.HandleFunc("/initialize", initializeHandler).Methods("POST")

	router.HandleFunc("/dapr/subscribe", configureSubscribeHandler).Methods("GET")

	router.HandleFunc("/"+pathA, eventHandlerA).Methods("POST")
	router.HandleFunc("/"+pathB, eventHandlerB).Methods("POST")
	router.HandleFunc("/"+pathC, eventHandlerC).Methods("POST")
	router.HandleFunc("/"+pathD, eventHandlerD).Methods("POST")
	router.HandleFunc("/"+pathE, eventHandlerE).Methods("POST")
	router.HandleFunc("/"+pathF, eventHandlerF).Methods("POST")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Dapr E2E test app: pubsub subscriber with routing - listening on http://localhost:%d", appPort)

	// initialize sets on application start
	initializeSets()

	server := http.Server{
		Addr:    fmt.Sprintf(":%d", appPort),
		Handler: appRouter(),
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
