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

	"github.com/dapr/dapr/tests/apps/utils"

	"github.com/gorilla/mux"
)

const (
	appPort              = 3000
	daprV1URL            = "http://localhost:3500/v1.0"
	actorMethodURLFormat = daprV1URL + "/actors/%s/%s/method/%s"
)

type daprActorResponse struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}

var httpClient = utils.NewHTTPClient()

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
}

// Invokes an actor and expects it to be successful.
func testCallActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s test request for %s", r.Method, r.URL.RequestURI())

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]
	method := mux.Vars(r)["method"]
	url := fmt.Sprintf(actorMethodURLFormat, actorType, id, method)

	body, err := httpCallAndVerify(r.Method, url, "fakereq", 200)
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

// Invokes an actor and forwards the response back (as a proxy).
func testCallActorProxyHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s test request for %s", r.Method, r.URL.RequestURI())

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]
	method := mux.Vars(r)["method"]
	url := fmt.Sprintf(actorMethodURLFormat, actorType, id, method)

	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Could not read request body: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	body, statusCode, headers, err := httpCall(r.Method, url, reqBody)
	if err != nil {
		log.Printf("Could not read actor's test response: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	for k, vals := range headers {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(statusCode)
	w.Write(body)
}

func httpCallAndVerify(method string, url string, requestBody interface{}, expectedHTTPStatusCode int) ([]byte, error) {
	body, statusCode, _, err := httpCall(method, url, requestBody)
	if err != nil {
		return nil, err
	}

	if statusCode != expectedHTTPStatusCode {
		t := fmt.Errorf("expected http status %d, received %d", expectedHTTPStatusCode, statusCode) //nolint:stylecheck
		return nil, t
	}

	return body, nil
}

func httpCall(method string, url string, requestBody interface{}) ([]byte, int, http.Header, error) {
	var body []byte
	var err error

	if requestBody != nil {
		body, err = json.Marshal(requestBody)
		if err != nil {
			return nil, 0, nil, err
		}
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, err
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}

	defer res.Body.Close()
	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, res.StatusCode, nil, err
	}

	return resBody, res.StatusCode, res.Header.Clone(), nil
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/proxy/{actorType}/{id}/method/{method}", testCallActorProxyHandler).Methods("POST", "DELETE")
	router.HandleFunc("/test/{actorType}/{id}/method/{method}", testCallActorHandler).Methods("POST", "DELETE")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Actor Client - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
