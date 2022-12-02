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
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/PaesslerAG/jsonpath"
	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/zipkin"

	"github.com/dapr/dapr/tests/apps/utils"
)

var (
	appPort      = 3000
	daprHTTPPort = 3500

	httpClient = utils.NewHTTPClient()

	httpMethods []string
)

const (
	zipkinEndpoint  = "http://dapr-zipkin:9411/api/v2/spans"
	jsonContentType = "application/json"
)

func init() {
	p := os.Getenv("DAPR_HTTP_PORT")
	if p != "" && p != "0" {
		daprHTTPPort, _ = strconv.Atoi(p)
	}
	p = os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
}

type appResponse struct {
	SpanName *string `json:"spanName,omitempty"`
	Message  string  `json:"message,omitempty"`
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// triggerInvoke is the handler for end-to-end to start the invoke stack
func triggerInvoke(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s %s", r.Method, r.URL.RequestURI())
	uuidObj, _ := uuid.NewRandom()
	uuid := uuidObj.String()
	newCtx, span := otel.Tracer(uuid).Start(context.TODO(), "triggerInvoke")
	defer span.End()

	appId := mux.Vars(r)["appId"]
	url := fmt.Sprintf("http://localhost:%s/v1.0/invoke/%s/method/invoke/something", strconv.Itoa(daprHTTPPort), appId)
	fmt.Printf("invoke url is %s\n", url)

	/* #nosec */
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	req = req.WithContext(newCtx)
	req.Header.Add("Content-Type", jsonContentType)
	_, err := httpClient.Do(req)
	if err != nil {
		log.Println(err)

		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(appResponse{
			SpanName: &uuid,
			Message:  err.Error(),
		})
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// invoke is the handler for end-to-end to invoke
func invoke(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s %s", r.Method, r.URL.RequestURI())
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// validate is the handler to validate the tracing span
func validate(w http.ResponseWriter, r *http.Request) {
	err := doValidate(w, r)
	if err != nil {
		log.Println(err)

		w.WriteHeader(http.StatusExpectationFailed)
		json.NewEncoder(w).Encode(appResponse{Message: err.Error()})
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

func doValidate(w http.ResponseWriter, r *http.Request) error {
	resp, err := http.Get(zipkinEndpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	v := interface{}(nil)

	json.NewDecoder(resp.Body).Decode(v)

	mainSpanId, err := jsonpath.Get("$..[?(@.name == \"example's main\")]['id'][0]", v)
	if err != nil {
		return err
	}

	mainSpanIdStr := fmt.Sprintf("%v", mainSpanId)
	_, err = jsonpath.Get("$..[?(@.parentId=='"+mainSpanIdStr+"' && @.name=='calllocal/invoke/something')]['id']", v)
	return nil
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/triggerInvoke", triggerInvoke).Methods("POST")
	router.HandleFunc("/invoke/something", invoke).Methods("POST", "GET")
	router.HandleFunc("/validate", validate).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Tracing App - listening on http://localhost:%d", appPort)

	exporter, err := zipkin.New(zipkinEndpoint)
	if err != nil {
		log.Fatalf("failed to create exporter: %v", err)
	}

	utils.StartServer(appPort, appRouter, true, false)
}
