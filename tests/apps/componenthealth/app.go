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

// kubernetes is the name of the secret store
const (
	componentHealthcheckURL = "http://localhost:3500/v1.0-alpha1/healthz/component"
)

// requestResponse represents a request or response for the APIs in this app.
type requestResponse struct {
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
	Message   string `json:"message,omitempty"`
}

type mockHealthResponse struct {
	Component string `json:"componentName"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	ErrorCode string `json:"errorCode,omitempty"`
	Message   string `json:"message,omitempty"`
}

type mockAllHealthResponse struct {
	Results []mockHealthResponse `json:"results"`
}

func indexHandler(w http.ResponseWriter, _ *http.Request) {
	log.Println("indexHandler is called")
	w.WriteHeader(http.StatusOK)
}

func getComponentsHealth(in string) (data mockAllHealthResponse, err error) {
	var healthResponse mockAllHealthResponse
	var res *http.Response
	var errGet error
	res, errGet = http.Get(componentHealthcheckURL)

	if errGet != nil {
		return healthResponse, fmt.Errorf("could not get component healthcheck %s", errGet.Error())
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return healthResponse, fmt.Errorf("could not load value for component healthcheck: %s", err.Error())
	}
	if res.StatusCode != http.StatusOK {
		log.Printf("Non 200 StatusCode: %d\n", res.StatusCode)

		return healthResponse, fmt.Errorf("got err response for get component healthcheck: %s", body)
	}
	err = json.Unmarshal(body, &healthResponse)
	return healthResponse, nil
}

// handles all APIs
func handler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing request for %s", r.URL.RequestURI())

	var healthResponse mockAllHealthResponse
	var err error
	res := requestResponse{}
	uri := r.URL.RequestURI()
	statusCode := http.StatusOK

	res.StartTime = epoch()

	cmd := mux.Vars(r)["command"]
	switch cmd {
	case "GetComponentHealthAlpha1":
		healthResponse, err = getComponentsHealth("ALL")
		if err != nil {
			statusCode = http.StatusInternalServerError
			res.Message = err.Error()
		}
	default:
		err = fmt.Errorf("invalid URI: %s", uri)
		statusCode = http.StatusBadRequest
		res.Message = err.Error()
	}

	res.EndTime = epoch()

	if statusCode != http.StatusOK {
		log.Printf("Error status code %v: %v", statusCode, res.Message)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(healthResponse)
}

// epoch returns the current unix epoch timestamp
func epoch() int {
	return (int)(time.Now().UTC().UnixNano() / 1000000)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/test/{command}", handler).Methods("GET")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func startServer() {
	// Create a server capable of supporting HTTP2 Cleartext connections
	// Also supports HTTP1.1 and upgrades from HTTP1.1 to HTTP2
	h2s := &http2.Server{}
	//nolint:gosec
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", appPort),
		Handler: h2c.NewHandler(appRouter(), h2s),
	}

	// Stop the server when we get a termination signal
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGINT) //nolint:staticcheck
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
	log.Printf("ComponentHealth App - listening on http://localhost:%d", appPort)
	log.Printf("ComponentHealth endpoint - to be served at %s", componentHealthcheckURL)
	startServer()
}
