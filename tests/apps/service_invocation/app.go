// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/gorilla/mux"
)

const (
	appPort  = 3000
	daprPort = 3500
)

type testCommandRequest struct {
	RemoteApp string `json:"remoteApp,omitempty"`
	Method    string `json:"method,omitempty"`
}

type appResponse struct {
	Message string `json:"message,omitempty"`
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

func singlehopHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "singlehop is called"})
}

func multihopHandler(w http.ResponseWriter, r *http.Request) {
	response, err := invokeService("serviceinvocation-callee-1", "singlehop")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func testHandler(w http.ResponseWriter, r *http.Request) {
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	response, err := invokeService(commandBody.RemoteApp, commandBody.Method)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func invokeService(remoteApp, method string) (appResponse, error) {
	url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/%s", daprPort, remoteApp, method)

	/* #nosec */
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		return appResponse{}, err
	}

	body, err := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	if err != nil {
		return appResponse{}, err
	}

	var appResp appResponse
	err = json.Unmarshal(body, &appResp)
	if err != nil {
		return appResponse{}, err
	}

	return appResp, nil
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/singlehop", singlehopHandler).Methods("POST")
	router.HandleFunc("/multihop", multihopHandler).Methods("POST")
	router.HandleFunc("/tests/invoke_test", testHandler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Hello Dapr - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
