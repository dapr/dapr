/*
Copyright 2026 The Dapr Authors
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
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const appPort = 3000

var (
	triggeredCount atomic.Int64
	httpClient     = &http.Client{Timeout: 30 * time.Second}
	daprBaseURL    = ""
)

func jobHandler(w http.ResponseWriter, r *http.Request) {
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	triggeredCount.Add(1)
	w.WriteHeader(http.StatusOK)
}

func triggeredCountHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, strconv.FormatInt(triggeredCount.Load(), 10))
}

func resetTriggeredCountHandler(w http.ResponseWriter, r *http.Request) {
	triggeredCount.Store(0)
	w.WriteHeader(http.StatusOK)
}

func scheduleJobHandler(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/scheduleJob/")
	if name == "" {
		http.Error(w, "missing job name", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, daprBaseURL+name, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("failed to copy sidecar response for job %q: %v", name, err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func main() {
	daprPort := os.Getenv("DAPR_HTTP_PORT")
	if daprPort == "" {
		daprPort = "3500"
	}
	daprBaseURL = "http://127.0.0.1:" + daprPort + "/v1.0/jobs/"

	http.HandleFunc("/job/", jobHandler)
	http.HandleFunc("/scheduleJob/", scheduleJobHandler)
	http.HandleFunc("/triggeredCount", triggeredCountHandler)
	http.HandleFunc("/resetTriggeredCount", resetTriggeredCountHandler)
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/", healthHandler)

	log.Printf("Jobs perf test app listening on port %d", appPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), nil))
}
