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
	"strings"
	"time"

	"github.com/dapr/dapr/tests/apps/utils"
	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "google.golang.org/grpc/examples/helloworld/helloworld"
	"google.golang.org/grpc/metadata"
)

type appResponse struct {
	Message string `json:"message,omitempty"`
}

type testCommandRequest struct {
	RemoteApp        string  `json:"remoteApp,omitempty"`
	Method           string  `json:"method,omitempty"`
	RemoteAppTracing string  `json:"remoteAppTracing"`
	Message          *string `json:"message"`
}

func run(w http.ResponseWriter, r *http.Request) {
	var request testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		log.Fatalf("could not decode request body: %v", err)
	}

	conn, err := grpc.Dial("localhost:50001",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(6*1024*1024), grpc.MaxCallSendMsgSize(6*1024*1024)),
	)
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewGreeterClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()

	ctx = metadata.AppendToOutgoingContext(ctx, "dapr-app-id", "grpcproxyserver")

	var name string
	if request.Method == "maxsize" {
		name = strings.Repeat("d", 5*1024*1024)
	} else {
		name = "Darth Tyranus"
	}

	resp, err := c.SayHello(ctx, &pb.HelloRequest{Name: name})
	if err != nil {
		log.Printf("could not greet: %v\n", err)
		w.WriteHeader(500)
		w.Write([]byte(fmt.Sprintf("failed to proxy request: %s", err)))
		return
	}

	if request.Method == "maxsize" {
		log.Printf("Message bytes exchanged: %d", len(resp.GetMessage()))
	} else {
		log.Printf("Greeting: %s", resp.GetMessage())
	}

	appResp := appResponse{
		Message: "success",
	}

	b, err := json.Marshal(appResp)
	if err != nil {
		log.Fatal(err)
	}

	w.WriteHeader(200)
	w.Write(b)
}

func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/tests/invoke_test", run).Methods("POST")

	return router
}

func main() {
	log.Printf("Hello Dapr - listening on http://localhost:%d", 3000)
	utils.StartServer(3000, appRouter, true, false)
}
