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
		t := fmt.Errorf("Expected http status %d, received %d", expectedHTTPStatusCode, res.StatusCode) //nolint:stylecheck
		return nil, t
	}

	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	return resBody, nil
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/test/{actorType}/{id}/method/{method}", testCallActorHandler).Methods("POST", "DELETE")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Actor Client - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
