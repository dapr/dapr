/*
Copyright 2022 The Dapr Authors
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
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort         = 3000
	securedAppPort  = 3001
	secretKey       = "secret-key"
	secretStoreName = "local-secret-store"
	bindingName     = "secured-binding"
	/* #nosec */
	secretURL     = "http://localhost:3500/v1.0/secrets/%s/%s?metadata.namespace=dapr-tests"
	bindingURL    = "http://localhost:3500/v1.0/bindings/%s"
	tlsCertEnvKey = "DAPR_TESTS_TLS_CERT"
	tlsKeyEnvKey  = "DAPR_TESTS_TLS_KEY"
)

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

func volumeMountTest() (int, appResponse) {
	log.Printf("volumeMountTest is called")

	// the secret store will be only able to get the value
	// if the volume is mounted correctly.
	url, err := url.Parse(fmt.Sprintf(secretURL, secretStoreName, secretKey))
	if err != nil {
		return http.StatusInternalServerError, appResponse{Message: fmt.Sprintf("Failed to parse secret url: %v", err)}
	}

	// get the secret value
	resp, err := http.Get(url.String())
	if err != nil {
		return http.StatusInternalServerError, appResponse{Message: fmt.Sprintf("Failed to get secret: %v", err)}
	}
	defer resp.Body.Close()

	// parse the secret value
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return http.StatusInternalServerError, appResponse{Message: fmt.Sprintf("Failed to read secret: %v", err)}
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("Non 200 StatusCode: %d\n", resp.StatusCode)
		return resp.StatusCode, appResponse{Message: fmt.Sprintf("Got error response for URL %s from Dapr: %v", url.String(), string(body))}
	}

	log.Printf("Found secret value: %s\n", string(body))

	state := map[string]string{}
	err = json.Unmarshal(body, &state)
	if err != nil {
		return http.StatusInternalServerError, appResponse{Message: fmt.Sprintf("Failed to unmarshal secret: %v", err)}
	}

	return http.StatusOK, appResponse{Message: state[secretKey]}
}

func bindingTest() (int, appResponse) {
	log.Printf("bindingTest is called")

	url, err := url.Parse(fmt.Sprintf(bindingURL, bindingName))
	if err != nil {
		return http.StatusInternalServerError, appResponse{Message: fmt.Sprintf("Failed to parse binding url: %v", err)}
	}

	// invoke the binding endpoint
	reqBody := "{\"operation\": \"get\"}"
	resp, err := http.Post(url.String(), "application/json", strings.NewReader(reqBody))
	if err != nil {
		return http.StatusInternalServerError, appResponse{Message: fmt.Sprintf("Failed to get binding: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Non 200 StatusCode: %d\n", resp.StatusCode)
		return resp.StatusCode, appResponse{Message: fmt.Sprintf("Got error response for URL %s from Dapr, status code: %v", url.String(), resp.StatusCode)}
	}

	return http.StatusOK, appResponse{Message: "OK"}
}

// commandHandler is the handler for end-to-end test entry point
// test driver code call this endpoint to trigger the test
func commandHandler(w http.ResponseWriter, r *http.Request) {
	testCommand := mux.Vars(r)["command"]

	// Trigger the test
	res := appResponse{Message: fmt.Sprintf("%s is not supported", testCommand)}
	statusCode := http.StatusBadRequest

	startTime := epoch()
	switch testCommand {
	case "testVolumeMount":
		statusCode, res = volumeMountTest()
	case "testBinding":
		statusCode, res = bindingTest()
	}
	res.StartTime = startTime
	res.EndTime = epoch()

	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(res)
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
	router.HandleFunc("/tests/{command}", commandHandler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func securedAppRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Injector App - listening on http://localhost:%d", appPort)

	// start a secured app (with TLS) on an alternate port
	go func() {
		os.Setenv(tlsCertEnvKey, "/tmp/testdata/certs/cert.pem")
		os.Setenv(tlsKeyEnvKey, "/tmp/testdata/certs/key.pem")
		utils.StartServer(securedAppPort, securedAppRouter, true, true)
	}()

	utils.StartServer(appPort, appRouter, true, false)
}
