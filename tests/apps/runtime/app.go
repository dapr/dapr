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
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort         = 3000
	pubsubHTTPTopic = "runtime-pubsub-http"
	bindingsTopic   = "runtime-bindings-http"
	daprHTTPAddr    = "localhost:3500"
	daprGRPCAddr    = "localhost:50001"
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
	DaprGRPCSuccess int `json:"dapr_grpc_success"`
	DaprGRPCError   int `json:"dapr_grpc_error"`
}

var (
	pubsubDaprHTTPError, pubsubDaprHTTPSuccess uint32
	pubsubDaprGRPCError, pubsubDaprGRPCSuccess uint32

	bindingsDaprHTTPError, bindingsDaprHTTPSuccess uint32
	bindingsDaprGRPCError, bindingsDaprGRPCSuccess uint32
)

var httpClient = utils.NewHTTPClient()

// indexHandler is the handler for root path.
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

func invokeDaprHTTPAPI() error {
	healthURL := fmt.Sprintf("http://%s/v1.0/healthz", daprHTTPAddr)
	r, err := httpClient.Get(healthURL)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	return nil
}

func invokeDaprGRPCAPI() error {
	// Dial the gRPC endpoint and fail if cannot connect in 10 seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx,
		daprGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock())
	if err != nil {
		return err
	}
	defer conn.Close()
	return nil
}

func testAPI(wg *sync.WaitGroup, successCount, errorCount *uint32, invoke func() error, id string) {
	defer wg.Done()

	err := invoke()
	if err != nil {
		log.Printf("Error calling Dapr %s API: %+v", id, err)
		atomic.AddUint32(errorCount, 1)
	} else {
		log.Printf("Success calling Dapr %s API", id)
		atomic.AddUint32(successCount, 1)
	}
}

func testHTTPAPI(wg *sync.WaitGroup, successCount, errorCount *uint32) {
	testAPI(wg, successCount, errorCount, invokeDaprHTTPAPI, "HTTP")
}

func testGRPCAPI(wg *sync.WaitGroup, successCount, errorCount *uint32) {
	testAPI(wg, successCount, errorCount, invokeDaprGRPCAPI, "gRPC")
}

// onPubsub handles messages published to "pubsub-http-server" and
// validates dapr's HTTP API is healthy.
func onPubsub(w http.ResponseWriter, r *http.Request) {
	log.Printf("onPubsub(): called %s\n", r.URL)

	var wg sync.WaitGroup

	wg.Add(1)
	go testHTTPAPI(&wg, &pubsubDaprHTTPSuccess, &pubsubDaprHTTPError)
	wg.Add(1)
	go testGRPCAPI(&wg, &pubsubDaprGRPCSuccess, &pubsubDaprGRPCError)

	wg.Wait()

	// Always return success as we want to release the messages
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{
		Message: "success",
	})
}

// onInputBinding handles incoming request from an input binding and
// validates dapr's HTTP API is healthy.
func onInputBinding(w http.ResponseWriter, r *http.Request) {
	log.Printf("onInputBinding(): called %s\n", r.URL)

	if r.Method == http.MethodOptions {
		log.Printf("%s binding input has been accepted", bindingsTopic)
		// Sending StatusOK back to the topic, so it will not attempt to redeliver.
		w.WriteHeader(http.StatusOK)
		return
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go testHTTPAPI(&wg, &bindingsDaprHTTPSuccess, &bindingsDaprHTTPError)
	wg.Add(1)
	go testGRPCAPI(&wg, &bindingsDaprGRPCSuccess, &bindingsDaprGRPCError)

	wg.Wait()

	w.WriteHeader(http.StatusOK)
}

func getPubsubDaprAPIResponse(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter getDaprAPIResponse")

	response := daprAPIResponse{
		DaprHTTPError:   int(pubsubDaprHTTPError),
		DaprHTTPSuccess: int(pubsubDaprHTTPSuccess),
		DaprGRPCError:   int(pubsubDaprGRPCError),
		DaprGRPCSuccess: int(pubsubDaprGRPCSuccess),
	}

	log.Printf("DaprAPIResponse=%+v", response)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func getBindingsDaprAPIResponse(w http.ResponseWriter, r *http.Request) {
	log.Println("Enter getDaprAPIResponse")

	response := daprAPIResponse{
		DaprHTTPError:   int(bindingsDaprHTTPError),
		DaprHTTPSuccess: int(bindingsDaprHTTPSuccess),
		DaprGRPCError:   int(bindingsDaprGRPCError),
		DaprGRPCSuccess: int(bindingsDaprGRPCSuccess),
	}

	log.Printf("DaprAPIResponse=%+v", response)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// appRouter initializes restful api router.
func appRouter() http.Handler {
	log.Printf("Enter appRouter()")
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

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
	utils.StartServer(appPort, appRouter, true, false)
}
