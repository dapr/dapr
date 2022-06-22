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
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	pb "google.golang.org/grpc/examples/helloworld/helloworld"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/anypb"
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

type PubsubResponse struct {
	// Status field for proper handling of errors form pubsub
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
}

var (
	daprClient   runtimev1pb.DaprClient
	callTracking map[string][]CallRecord
)

var httpClient = utils.NewHTTPClient()

// Endpoint handling.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler() called")
	w.WriteHeader(http.StatusOK)
}

func healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func unknownHandler(w http.ResponseWriter, r *http.Request) {
	// Just some debugging, if this is printed the app isn't setup correctly.
	log.Printf("Unknown route called: %s", r.RequestURI)
	w.WriteHeader(http.StatusNotFound)
}

func configureSubscribeHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("/dapr/subscribe called")

	subscriptions := []struct {
		PubsubName string
		Topic      string
		Route      string
	}{
		{
			PubsubName: "dapr-resiliency-pubsub",
			Topic:      "resiliency-topic-http",
			Route:      "resiliency-topic-http",
		},
	}
	b, err := json.Marshal(subscriptions)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

func actorConfigHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("/dapr/config called")
	daprConfig := struct {
		Entities []string
	}{
		Entities: []string{"resiliencyActor"},
	}

	b, err := json.Marshal(daprConfig)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

func resiliencyBindingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		log.Println("resiliency binding input has been accepted")

		w.WriteHeader(http.StatusOK)
		return
	}

	var message FailureMessage
	json.NewDecoder(r.Body).Decode(&message)

	log.Printf("Binding received message %+v\n", message)

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

func resiliencyPubsubHandler(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(PubsubResponse{
			Message: "No body",
			Status:  "DROP",
		})
	}
	defer r.Body.Close()

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(PubsubResponse{
			Message: "Couldn't read body",
			Status:  "DROP",
		})
	}

	log.Printf("Raw body: %s", string(body))

	var rawBody map[string]interface{}
	json.Unmarshal(body, &rawBody)

	rawData := rawBody["data"].(map[string]interface{})
	rawDataBytes, _ := json.Marshal(rawData)
	var message FailureMessage
	json.Unmarshal(rawDataBytes, &message)
	log.Printf("Pubsub received message %+v\n", message)

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
	json.NewEncoder(w).Encode(PubsubResponse{
		Message: "consumed",
		Status:  "SUCCESS",
	})
}

func resiliencyServiceInvocationHandler(w http.ResponseWriter, r *http.Request) {
	var message FailureMessage
	json.NewDecoder(r.Body).Decode(&message)

	log.Printf("Http invocation received message %+v\n", message)

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

func resiliencyActorMethodHandler(w http.ResponseWriter, r *http.Request) {
	var message FailureMessage
	json.NewDecoder(r.Body).Decode(&message)

	log.Printf("Actor received message %+v\n", message)

	callCount := 0
	if records, ok := callTracking[message.ID]; ok {
		callCount = records[len(records)-1].Count + 1
	}

	log.Printf("Seen %s %d times.", message.ID, callCount)

	callTracking[message.ID] = append(callTracking[message.ID], CallRecord{Count: callCount, TimeSeen: time.Now()})
	if message.MaxFailureCount != nil && callCount < *message.MaxFailureCount {
		if message.Timeout != nil {
			// This request can still succeed if the resiliency policy timeout is longer than this sleep.
			log.Printf("Sleeping: %v", *message.Timeout)
			time.Sleep(*message.Timeout)
		} else {
			log.Println("Forcing failure.")
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

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")

	// Calls from dapr.
	router.HandleFunc("/dapr/subscribe", configureSubscribeHandler).Methods("GET")
	router.HandleFunc("/dapr/config", actorConfigHandler).Methods("GET")
	router.HandleFunc("/healthz", healthz).Methods("GET")

	// Handling events/methods.
	router.HandleFunc("/resiliencybinding", resiliencyBindingHandler).Methods("POST", "OPTIONS")
	router.HandleFunc("/resiliency-topic-http", resiliencyPubsubHandler).Methods("POST")
	router.HandleFunc("/resiliencyInvocation", resiliencyServiceInvocationHandler).Methods("POST")
	router.HandleFunc("/actors/{actorType}/{id}/method/{method}", resiliencyActorMethodHandler).Methods("PUT")

	// Test functions.
	router.HandleFunc("/tests/getCallCount", TestGetCallCount).Methods("GET")
	router.HandleFunc("/tests/getCallCountGRPC", TestGetCallCountGRPC).Methods("GET")
	router.HandleFunc("/tests/invokeBinding/{binding}", TestInvokeOutputBinding).Methods("POST")
	router.HandleFunc("/tests/publishMessage/{pubsub}/{topic}", TestPublishMessage).Methods("POST")
	router.HandleFunc("/tests/invokeService/{protocol}", TestInvokeService).Methods("POST")
	router.HandleFunc("/tests/invokeActor/{protocol}", TestInvokeActorMethod).Methods("POST")

	router.NotFoundHandler = router.NewRoute().HandlerFunc(unknownHandler).GetHandler()

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	callTracking = map[string][]CallRecord{}
	initGRPCClient()

	log.Printf("Resiliency App - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true)
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
			Data:   &anypb.Any{},
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

func TestPublishMessage(w http.ResponseWriter, r *http.Request) {
	pubsub := mux.Vars(r)["pubsub"]
	topic := mux.Vars(r)["topic"]

	var message FailureMessage
	err := json.NewDecoder(r.Body).Decode(&message)
	if err != nil {
		log.Println("Could not parse message.")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	log.Printf("Publishing to %s/%s - %+v", pubsub, topic, message)
	b, _ := json.Marshal(message)

	req := &runtimev1pb.PublishEventRequest{
		PubsubName:      pubsub,
		Topic:           topic,
		Data:            b,
		DataContentType: "application/json",
	}

	_, err = daprClient.PublishEvent(context.Background(), req)
	if err != nil {
		log.Printf("Error publishing event: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func TestInvokeService(w http.ResponseWriter, r *http.Request) {
	protocol := mux.Vars(r)["protocol"]
	log.Printf("Invoking resiliency service with %s", protocol)

	if protocol == "http" {
		url := "http://localhost:3500/v1.0/invoke/resiliencyapp/method/resiliencyInvocation"

		req, _ := http.NewRequest("POST", url, r.Body)
		defer r.Body.Close()

		resp, err := httpClient.Do(req)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(resp.StatusCode)

		if resp.Body != nil {
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			w.Write(b)
		}
	} else if protocol == "grpc" {
		var message FailureMessage
		err := json.NewDecoder(r.Body).Decode(&message)
		if err != nil {
			log.Println("Could not parse message.")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		b, _ := json.Marshal(message)

		req := &runtimev1pb.InvokeServiceRequest{
			Id: "resiliencyappgrpc",
			Message: &commonv1pb.InvokeRequest{
				Method: "grpcInvoke",
				Data: &anypb.Any{
					Value: b,
				},
			},
		}

		_, err = daprClient.InvokeService(r.Context(), req)
		if err != nil {
			log.Printf("Failed to invoke service: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf("failed to call grpc service: %s", err.Error())))
			return
		}
	} else if protocol == "grpc_proxy" {
		var message FailureMessage
		err := json.NewDecoder(r.Body).Decode(&message)
		if err != nil {
			log.Println("Could not parse message.")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		log.Printf("Proxying message: %+v", message)
		b, _ := json.Marshal(message)

		conn, err := grpc.Dial("localhost:50001", grpc.WithInsecure(), grpc.WithBlock())
		if err != nil {
			log.Fatalf("did not connect: %v", err)
		}
		defer conn.Close()
		client := pb.NewGreeterClient(conn)

		ctx := r.Context()
		ctx = metadata.AppendToOutgoingContext(ctx, "dapr-app-id", "resiliencyappgrpc")
		_, err = client.SayHello(ctx, &pb.HelloRequest{Name: string(b)})
		if err != nil {
			log.Printf("could not proxy request: %s", err.Error())
			w.WriteHeader(500)
			w.Write([]byte(fmt.Sprintf("failed to proxy request: %s", err.Error())))
			return
		}
	}
}

func TestInvokeActorMethod(w http.ResponseWriter, r *http.Request) {
	protocol := mux.Vars(r)["protocol"]
	log.Printf("Invoking resiliency actor with %s", protocol)

	if protocol == "http" {
		httpClient.Timeout = time.Minute
		url := "http://localhost:3500/v1.0/actors/resiliencyActor/1/method/resiliencyMethod"

		req, _ := http.NewRequest("PUT", url, r.Body)
		defer r.Body.Close()

		resp, err := httpClient.Do(req)
		if err != nil {
			log.Printf("An error occurred calling actors: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(resp.StatusCode)
	} else if protocol == "grpc" {
		var message FailureMessage
		err := json.NewDecoder(r.Body).Decode(&message)
		if err != nil {
			log.Println("Could not parse message.")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		b, _ := json.Marshal(message)

		req := &runtimev1pb.InvokeActorRequest{
			ActorType: "resiliencyActor",
			ActorId:   "1",
			Method:    "resiliencyMethod",
			Data:      b,
		}

		_, err = daprClient.InvokeActor(r.Context(), req)
		if err != nil {
			log.Printf("An error occurred calling actors: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
}
