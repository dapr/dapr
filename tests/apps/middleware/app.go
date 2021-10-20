// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/gorilla/mux"
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

func main() {
	log.Printf("Middleware App - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
