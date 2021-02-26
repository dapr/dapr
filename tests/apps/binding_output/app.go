// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/golang/protobuf/ptypes/any"

	"google.golang.org/grpc"

	"github.com/gorilla/mux"
)

const (
	appPort      = 3000
	daprPort     = 3500
	daprPortGRPC = 50001
)

type testCommandRequest struct {
	Messages []struct {
		Data      string `json:"data,omitempty"`
		Operation string `json:"operation"`
	} `json:"messages,omitempty"`
}

type indexHandlerResponse struct {
	Message string `json:"message,omitempty"`
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(indexHandlerResponse{Message: "OK"})
}

func testHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Entered testHandler")
	var requestBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&requestBody)
	if err != nil {
		log.Printf("error parsing request body: %s", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	url := fmt.Sprintf("http://localhost:%d/v1.0/bindings/test-topic", daprPort)

	for _, message := range requestBody.Messages {
		body, err := json.Marshal(&message)
		if err != nil {
			log.Printf("error encoding message: %s", err)

			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error: " + err.Error()))
			return
		}

		/* #nosec */
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
		resp.Body.Close()

		if err != nil {
			log.Printf("error sending request to output binding: %s", err)

			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error: " + err.Error()))
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

func sendGRPC(w http.ResponseWriter, r *http.Request) {
	log.Printf("Entered sendGRPC")
	var requestBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&requestBody)
	if err != nil {
		log.Printf("error parsing request body: %s", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	conn, err := grpc.Dial(fmt.Sprintf("localhost:%d", daprPortGRPC), grpc.WithInsecure())
	if err != nil {
		log.Printf("Could not make dapr client: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	client := runtimev1pb.NewDaprClient(conn)

	for _, message := range requestBody.Messages {
		body, _ := json.Marshal(&message)

		log.Printf("Sending message: %s", body)
		req := runtimev1pb.InvokeBindingRequest{
			Name:      "test-topic-grpc",
			Data:      body,
			Operation: "create",
		}

		_, err = client.InvokeBinding(context.Background(), &req)
		if err != nil {
			log.Printf("Error sending request to GRPC output binding: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error: " + err.Error()))
			return
		}
	}
}

func getReceivedTopicsGRPC(w http.ResponseWriter, r *http.Request) {
	log.Printf("Entered getReceivedTopicsGRPC")

	conn, err := grpc.Dial(fmt.Sprintf("localhost:%d", daprPortGRPC), grpc.WithInsecure())
	if err != nil {
		log.Printf("Could not make dapr client: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	client := runtimev1pb.NewDaprClient(conn)

	req := runtimev1pb.InvokeServiceRequest{
		Id: "bindinginputgrpc",
		Message: &commonv1pb.InvokeRequest{
			Method: "GetReceivedTopics",
			Data:   &any.Any{},
			HttpExtension: &commonv1pb.HTTPExtension{
				Verb: commonv1pb.HTTPExtension_POST,
			},
		},
	}

	resp, err := client.InvokeService(context.Background(), &req)
	if err != nil {
		log.Printf("Could not get received messages: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(resp.Data.Value)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/tests/send", testHandler).Methods("POST")
	router.HandleFunc("/tests/sendGRPC", sendGRPC).Methods("POST")
	router.HandleFunc("/tests/get_received_topics_grpc", getReceivedTopicsGRPC).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
