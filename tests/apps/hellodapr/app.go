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
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const appPort = 3000

type testCommandRequest struct {
	Message string `json:"message,omitempty"`
}

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// testHandler is the handler for end-to-end test entry point
// test driver code call this endpoint to trigger the test
func testHandler(w http.ResponseWriter, r *http.Request) {
	testCommand := mux.Vars(r)["test"]

	// Retrieve request body contents
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	// Trigger the test
	res := appResponse{Message: fmt.Sprintf("%s is not supported", testCommand)}
	statusCode := http.StatusBadRequest

	startTime := epoch()
	switch testCommand {
	case "blue":
		statusCode, res = blueTest(commandBody)
	case "green":
		statusCode, res = greenTest(commandBody)
	case "envTest":
		statusCode, res = envTest(commandBody)
	}
	res.StartTime = startTime
	res.EndTime = epoch()

	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(res)
}

func greenTest(commandRequest testCommandRequest) (int, appResponse) {
	log.Printf("GreenTest - message: %s", commandRequest.Message)
	return http.StatusOK, appResponse{Message: "Hello green dapr!"}
}

func blueTest(commandRequest testCommandRequest) (int, appResponse) {
	log.Printf("BlueTest - message: %s", commandRequest.Message)
	return http.StatusOK, appResponse{Message: "Hello blue dapr!"}
}

func envTest(commandRequest testCommandRequest) (int, appResponse) {
	log.Printf("envTest - message: %s", commandRequest.Message)
	daprHTTPPort, ok := os.LookupEnv("DAPR_HTTP_PORT")
	if !ok {
		log.Println("Expected DAPR_HTTP_PORT to be set.")
	}
	daprGRPCPort, ok := os.LookupEnv("DAPR_GRPC_PORT")
	if !ok {
		log.Println("Expected DAPR_GRPC_PORT to be set.")
	}
	return http.StatusOK, appResponse{Message: fmt.Sprintf("%s %s", daprHTTPPort, daprGRPCPort)}
}

// epoch returns the current unix epoch timestamp
func epoch() int {
	return (int)(time.Now().UTC().UnixNano() / 1000000)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/tests/{test}", testHandler).Methods("POST")

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
