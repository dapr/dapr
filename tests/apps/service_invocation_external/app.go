/*
Copyright 2023 The Dapr Authors
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

//nolint:forbidigo
package main

import (
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

const (
	jsonContentType = "application/json"
)

type httpTestMethods struct {
	Verb       string
	SendBody   bool
	ExpectBody bool
}

var testMethods = []httpTestMethods{
	{
		Verb:       "GET",
		SendBody:   false,
		ExpectBody: true,
	},
	{
		Verb:       "HEAD",
		SendBody:   false,
		ExpectBody: false,
	},
	{
		Verb:       "POST",
		SendBody:   true,
		ExpectBody: true,
	},
	{
		Verb:       "PUT",
		SendBody:   true,
		ExpectBody: true,
	},
	{
		Verb:       "DELETE",
		SendBody:   true,
		ExpectBody: true,
	},
	// Go's net/http library does not support sending requests with the CONNECT method
	/*{
		Verb:       "CONNECT",
		Callback:   "connecthandler",
		SendBody:   true,
		ExpectBody: true,
	},*/
	{
		Verb:       "OPTIONS",
		SendBody:   true,
		ExpectBody: true,
	},
	{
		Verb:       "TRACE",
		SendBody:   true,
		ExpectBody: true,
	},
	{
		Verb:       "PATCH",
		SendBody:   true,
		ExpectBody: true,
	},
}

var appPort = 3000

type appResponse struct {
	Message string `json:"message,omitempty"`
}

func init() {
	p := os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
}

func main() {
	log.Printf("service_invocation_external - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/retrieve_request_object", retrieveRequestObject).Methods("POST")
	// these are called through dapr service invocation
	for _, test := range testMethods {
		if test.SendBody {
			router.HandleFunc("/externalInvocation", withBodyHandler).Methods(test.Verb)
		} else {
			router.HandleFunc("/externalInvocation", noBodyHandler).Methods(test.Verb)
		}
	}
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func retrieveRequestObject(w http.ResponseWriter, r *http.Request) {
	headers := map[string][]string{}
	for k, vals := range r.Header {
		headers[k] = vals
		log.Printf("headers: %s %q", k, vals)
	}

	serializedHeaders, _ := json.Marshal(headers)

	w.Header().Set("Content-Type", jsonContentType)
	w.Header().Set("DaprTest-Response-1", "DaprTest-Response-Value-1")
	w.Header().Set("DaprTest-Response-2", "DaprTest-Response-Value-2")
	w.Header().Add("DaprTest-Response-Multi", "DaprTest-Response-Multi-1")
	w.Header().Add("DaprTest-Response-Multi", "DaprTest-Response-Multi-2")

	if val, ok := headers["Daprtest-Traceid"]; ok {
		// val[0] is client app given trace id
		w.Header().Set("traceparent", val[0])
	}
	w.WriteHeader(http.StatusOK)
	w.Write(serializedHeaders)
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "success"})
}

// Bad http request
func onBadRequest(w http.ResponseWriter, err error) {
	msg := "deserialization failed with " + err.Error()
	logAndSetResponse(w, http.StatusBadRequest, msg)
}

func onDeserializationFailed(w http.ResponseWriter, err error) {
	msg := "deserialization failed with " + err.Error()
	logAndSetResponse(w, http.StatusInternalServerError, msg)
}

func logAndSetResponse(w http.ResponseWriter, statusCode int, message string) {
	log.Println(message)

	w.WriteHeader(statusCode)
	json.NewEncoder(w).
		Encode(appResponse{Message: message})
}

// Handles a request with a JSON body.  Extracts s string from the input json and returns in it an appResponse.
func withBodyHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("withBodyHandler called. HTTP Verb: %s\n", r.Method)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		onBadRequest(w, err)
		return
	}
	fmt.Printf("withBodyHandler body: %s\n", string(body))
	var s string
	err = json.Unmarshal(body, &s)
	if err != nil {
		onDeserializationFailed(w, err)
		return
	}

	w.Header().Add("x-dapr-tests-request-method", r.Method)
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(appResponse{Message: s})
}

// Handles a request with no body.  Returns an appResponse with appResponse.Message "ok", which caller validates.
func noBodyHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("noBodyHandler called. HTTP Verb: %s \n", r.Method)
	w.Header().Add("x-dapr-tests-request-method", r.Method)

	logAndSetResponse(w, http.StatusOK, "success")
}
