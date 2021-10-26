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
	"net"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

const (
	appPort               = 3000
	daprV1URL             = "http://localhost:3500/v1.0"
	actorMethodURLFormat  = daprV1URL + "/actors/%s/%s/method/%s"
	secondsToWaitInMethod = 5
)

type daprActorResponse struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}

var httpClient = newHTTPClient()

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
}

// nolint:gosec
func testCallActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s test request for %s", r.Method, r.URL.RequestURI())

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]
	method := mux.Vars(r)["method"]
	url := fmt.Sprintf(actorMethodURLFormat, actorType, id, method)

	body, err := httpCall(r.Method, url, "fakereq", 200)
	if err != nil {
		log.Printf("Could not read actor's test response: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if len(body) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	var response daprActorResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		log.Printf("Could not parse actor's test response: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Write(response.Data)
}

func httpCall(method string, url string, requestBody interface{}, expectedHTTPStatusCode int) ([]byte, error) {
	var body []byte
	var err error

	if requestBody != nil {
		body, err = json.Marshal(requestBody)
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer res.Body.Close()

	if res.StatusCode != expectedHTTPStatusCode {
		t := fmt.Errorf("Expected http status %d, received %d", expectedHTTPStatusCode, res.StatusCode)
		return nil, t
	}

	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	return resBody, nil
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// epoch returns the current unix epoch timestamp
func epoch() int {
	return (int)(time.Now().UTC().UnixNano() / 1000000)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/test/{actorType}/{id}/method/{method}", testCallActorHandler).Methods("POST", "DELETE")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func newHTTPClient() http.Client {
	dialer := &net.Dialer{ //nolint:exhaustivestruct
		Timeout: 5 * time.Second,
	}
	netTransport := &http.Transport{ //nolint:exhaustivestruct
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
	}

	return http.Client{ //nolint:exhaustivestruct
		Timeout:   30 * time.Second,
		Transport: netTransport,
	}
}

func main() {
	log.Printf("Actor Client - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
