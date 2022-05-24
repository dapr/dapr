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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

const (
	appPort     = 3000
	daprBaseURL = "http://localhost:3500/v1.0"
)

type testResponse struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// indexHandler is the handler for root path.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
}

func testLogCall(w http.ResponseWriter, r *http.Request) {
	log.Printf("testLogCall is called")

	service := mux.Vars(r)["service"]

	input := "hello"
	url := fmt.Sprintf("%s/invoke/%s/method/logCall", daprBaseURL, service)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer([]byte(input))) // nolint:gosec
	if err != nil {
		log.Printf("Could not call service")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	results := testResponse{
		input, string(body),
	}

	outputBytes, _ := json.Marshal(results)
	w.Write(outputBytes)
	w.WriteHeader(resp.StatusCode)
}

func logCall(w http.ResponseWriter, r *http.Request) {
	log.Printf("logCall is called")

	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)

	log.Printf("Got: %s", string(body))
	w.Write(body)
}

// appRouter initializes restful api router.
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/test/logCall/{service}", testLogCall).Methods("POST")
	router.HandleFunc("/logCall", logCall).Methods("POST")
	router.HandleFunc("/healthz", healthzHandler).Methods("GET")

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
	log.Printf("Middleware App - listening on http://localhost:%d", appPort)
	startServer()
}
