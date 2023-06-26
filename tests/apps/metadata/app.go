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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort = 3000
	/* #nosec */
	metadataURL = "http://localhost:3500/v1.0/metadata"
)

var httpClient = utils.NewHTTPClient()

// requestResponse represents a request or response for the APIs in this app.
type requestResponse struct {
	StartTime time.Time `json:"start_time,omitempty"`
	EndTime   time.Time `json:"end_time,omitempty"`
	Message   string    `json:"message,omitempty"`
}

type setMetadataRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type mockMetadata struct {
	ID                   string                    `json:"id"`
	ActiveActorsCount    []activeActorsCount       `json:"actors"`
	Extended             map[string]string         `json:"extended"`
	RegisteredComponents []mockRegisteredComponent `json:"components"`
	EnabledFeatures      []string                  `json:"enabledFeatures"`
}

type activeActorsCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type mockRegisteredComponent struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

func indexHandler(w http.ResponseWriter, _ *http.Request) {
	log.Println("indexHandler is called")
	w.WriteHeader(http.StatusOK)
}

func setMetadata(r *http.Request) error {
	var data setMetadataRequest
	err := json.NewDecoder(r.Body).Decode(&data)
	if err != nil {
		return err
	}

	if data.Key == "" || data.Value == "" {
		return errors.New("key or value in request must be set")
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPut,
		metadataURL+"/"+data.Key,
		strings.NewReader(data.Value),
	)
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "text/plain")
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("invalid status code: %d", res.StatusCode)
	}

	return nil
}

func getMetadata() (data mockMetadata, err error) {
	var metadata mockMetadata
	res, err := httpClient.Get(metadataURL)
	if err != nil {
		return metadata, fmt.Errorf("could not get sidecar metadata %s", err.Error())
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return metadata, fmt.Errorf("could not load value for Metadata: %s", err.Error())
	}
	if res.StatusCode != http.StatusOK {
		log.Printf("Non 200 StatusCode: %d\n", res.StatusCode)

		return metadata, fmt.Errorf("got err response for get Metadata: %s", body)
	}
	err = json.Unmarshal(body, &metadata)
	return metadata, nil
}

// handles all APIs
func handler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing request for %s", r.URL.RequestURI())

	var metadata mockMetadata
	var err error
	res := requestResponse{}
	uri := r.URL.RequestURI()
	statusCode := http.StatusOK

	res.StartTime = time.Now()

	cmd := mux.Vars(r)["command"]
	switch cmd {
	case "setMetadata":
		err = setMetadata(r)
		if err != nil {
			statusCode = http.StatusInternalServerError
			res.Message = err.Error()
		}
		res.Message = "ok"
	case "getMetadata":
		metadata, err = getMetadata()
		if err != nil {
			statusCode = http.StatusInternalServerError
			res.Message = err.Error()
		}
	default:
		err = fmt.Errorf("invalid URI: %s", uri)
		statusCode = http.StatusBadRequest
		res.Message = err.Error()
	}

	res.EndTime = time.Now()

	if statusCode != http.StatusOK {
		log.Printf("Error status code %v: %v", statusCode, res.Message)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if res.Message == "" {
		json.NewEncoder(w).Encode(metadata)
	} else {
		json.NewEncoder(w).Encode(res)
	}
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/test/{command}", handler)

	return router
}

func main() {
	log.Printf("Metadata App - listening on http://localhost:%d", appPort)
	log.Printf("Metadata endpoint - to be served at %s", metadataURL)
	utils.StartServer(appPort, appRouter, true, false)
}
