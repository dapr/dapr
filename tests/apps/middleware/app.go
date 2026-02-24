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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/dapr/dapr/tests/apps/utils"
)

var (
	appPort  = 3000
	daprPort = 3500
)

func init() {
	p := os.Getenv("DAPR_HTTP_PORT")
	if p != "" && p != "0" {
		daprPort, _ = strconv.Atoi(p)
	}
	p = os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
}

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
	url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/logCall", daprPort, service)
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(input))) //nolint:gosec
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

	w.WriteHeader(resp.StatusCode)
	_ = json.NewEncoder(w).Encode(results)
}

func logCall(w http.ResponseWriter, r *http.Request) {
	log.Printf("logCall is called")

	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)

	log.Printf("Got: %s", string(body))
	w.Write(body)
}

// appRouter initializes restful api router.
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/test/logCall/{service}", testLogCall).Methods("POST")
	router.HandleFunc("/logCall", logCall).Methods("POST")
	router.HandleFunc("/healthz", healthzHandler).Methods("GET")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Middleware App - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
