// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
)

const (
	appPort  = 3000
	daprPort = 3500
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

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/tests/send", testHandler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
