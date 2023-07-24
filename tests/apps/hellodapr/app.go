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
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"github.com/dapr/dapr/tests/apps/utils"
)

var appPort = 3000

func init() {
	p := os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
}

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
	return int(time.Now().UnixMilli())
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/tests/{test}", testHandler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
