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
	"time"

	"github.com/golang/protobuf/ptypes/any"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
)

const (
	appPort = 3000
)

type FailureMessage struct {
	ID              string         `json:"id"`
	MaxFailureCount *int           `json:"maxFailureCount,omitempty"`
	Timeout         *time.Duration `json:"timeout,omitempty"`
}

type CallRecord struct {
	Count    int
	TimeSeen time.Time
}

var (
	daprClient   runtimev1pb.DaprClient
	callTracking map[string][]CallRecord
)

// Endpoint handling.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler() called")
	w.WriteHeader(http.StatusOK)
}

func resiliencyBindingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		log.Println("resiliency binding input has been accepted")

		w.WriteHeader(http.StatusOK)
		return
	}

	var message FailureMessage
	json.NewDecoder(r.Body).Decode(&message)

	log.Printf("Received message %+v\n", message)

	callCount := 0
	if records, ok := callTracking[message.ID]; ok {
		callCount = records[len(records)-1].Count + 1
	}

	log.Printf("Seen %s %d times.", message.ID, callCount)

	callTracking[message.ID] = append(callTracking[message.ID], CallRecord{Count: callCount, TimeSeen: time.Now()})
	if message.MaxFailureCount != nil && callCount < *message.MaxFailureCount {
		if message.Timeout != nil {
			// This request can still succeed if the resiliency policy timeout is longer than this sleep.
			time.Sleep(*message.Timeout)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// App startup/endpoint setup.
func initGRPCClient() {
	url := fmt.Sprintf("localhost:%d", 50001)
	log.Printf("Connecting to dapr using url %s", url)
	var grpcConn *grpc.ClientConn
	for retries := 10; retries > 0; retries-- {
		var err error
		grpcConn, err = grpc.Dial(url, grpc.WithInsecure())
		if err == nil {
			break
		}

		if retries == 0 {
			log.Printf("Could not connect to dapr: %v", err)
			log.Panic(err)
		}

		log.Printf("Could not connect to dapr: %v, retrying...", err)
		time.Sleep(5 * time.Second)
	}

	daprClient = runtimev1pb.NewDaprClient(grpcConn)
}

func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/resiliencybinding", resiliencyBindingHandler).Methods("POST", "OPTIONS")
	router.HandleFunc("/tests/getCallCount", TestGetCallCount).Methods("GET")
	router.HandleFunc("/tests/getCallCountGRPC", TestGetCallCountGRPC).Methods("GET")
	router.HandleFunc("/tests/invokeBinding/{binding}", TestInvokeOutputBinding).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr - listening on http://localhost:%d", appPort)
	callTracking = map[string][]CallRecord{}
	initGRPCClient()

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}

// Test Functions.
func TestGetCallCount(w http.ResponseWriter, r *http.Request) {
	log.Println("Getting call counts")
	for key, val := range callTracking {
		log.Printf("\t%s - Called %d times.\n", key, len(val))
	}

	b, err := json.Marshal(callTracking)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

func TestGetCallCountGRPC(w http.ResponseWriter, r *http.Request) {
	log.Printf("Getting call counts for gRPC")

	req := runtimev1pb.InvokeServiceRequest{
		Id: "resiliencyappgrpc",
		Message: &commonv1pb.InvokeRequest{
			Method: "GetCallCount",
			Data:   &any.Any{},
			HttpExtension: &commonv1pb.HTTPExtension{
				Verb: commonv1pb.HTTPExtension_POST,
			},
		},
	}

	resp, err := daprClient.InvokeService(context.Background(), &req)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(resp.Data.Value)
}

func TestInvokeOutputBinding(w http.ResponseWriter, r *http.Request) {
	binding := mux.Vars(r)["binding"]
	log.Printf("Making call to output binding %s.", binding)

	var message FailureMessage
	err := json.NewDecoder(r.Body).Decode(&message)
	if err != nil {
		log.Println("Could not parse message.")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	b, _ := json.Marshal(message)
	req := &runtimev1pb.InvokeBindingRequest{
		Name:      binding,
		Operation: "create",
		Data:      b,
	}

	_, err = daprClient.InvokeBinding(context.Background(), req)
	if err != nil {
		log.Printf("Error invoking binding: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
	}
}
