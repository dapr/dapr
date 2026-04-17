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
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
)

const appPort = 3000

var triggeredCount atomic.Int64

// jobHandler receives triggered jobs from the Dapr sidecar.
func jobHandler(w http.ResponseWriter, r *http.Request) {
	triggeredCount.Add(1)
	w.WriteHeader(http.StatusOK)
}

// triggeredCountHandler returns the number of triggered jobs.
func triggeredCountHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, strconv.FormatInt(triggeredCount.Load(), 10))
}

// resetTriggeredCountHandler resets the triggered count to zero.
func resetTriggeredCountHandler(w http.ResponseWriter, r *http.Request) {
	triggeredCount.Store(0)
	w.WriteHeader(http.StatusOK)
}

// healthHandler returns a 200 OK for health checks.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func main() {
	// Receive triggered jobs from Dapr sidecar at /job/{name}
	http.HandleFunc("/job/", jobHandler)
	http.HandleFunc("/triggeredCount", triggeredCountHandler)
	http.HandleFunc("/resetTriggeredCount", resetTriggeredCountHandler)
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/", healthHandler)

	log.Printf("Jobs perf test app listening on port %d", appPort)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), nil))
}
