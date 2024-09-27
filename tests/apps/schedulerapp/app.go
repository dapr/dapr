/*
Copyright 2024 The Dapr Authors
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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"

	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort      = 3000
	daprPortHTTP = 3500
)

type triggeredJob struct {
	TypeURL string `json:"type_url"`
	Value   string `json:"value"`
}

type jobData struct {
	DataType string `json:"@type"`
	Value    string `json:"value"`
}

type job struct {
	Data     jobData `json:"data,omitempty"`
	Schedule string  `json:"schedule,omitempty"`
	Repeats  int     `json:"repeats,omitempty"`
	DueTime  string  `json:"dueTime,omitempty"`
	TTL      string  `json:"ttl,omitempty"`
}

var (
	httpClient = utils.NewHTTPClient()

	triggeredJobs []triggeredJob
	jobsMutex     sync.Mutex
)

func makeHTTPCall(jobName string, body []byte, methodType string) ([]byte, int, error) {
	url := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/jobs/%s", daprPortHTTP, jobName)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, methodType, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		if resp != nil {
			return nil, resp.StatusCode, err
		}
		return nil, http.StatusInternalServerError, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	return respBody, resp.StatusCode, nil
}

// scheduleJobHandler is to schedule a job with the Daprd sidecar
func scheduleJobHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the job name from the URL path parameters
	vars := mux.Vars(r)
	jobName := vars["name"]

	// Extract job data from the request body
	var j job
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		http.Error(w, fmt.Sprintf("error decoding JSON: %v", err), http.StatusBadRequest)
		return
	}

	jsonData, err := json.Marshal(j)
	if err != nil {
		http.Error(w, fmt.Sprintf("error encoding JSON: %v", err), http.StatusInternalServerError)
		return
	}

	log.Printf("Scheduling job named: %s", jobName)
	_, statusCode, err := makeHTTPCall(jobName, jsonData, http.MethodPost)
	if err != nil {
		http.Error(w, fmt.Sprintf("error scheduling job: %v", err), statusCode)
		return
	}

	w.WriteHeader(statusCode)
}

// addTriggeredJob appends the triggered job to the global slice
func addTriggeredJob(job triggeredJob) {
	jobsMutex.Lock()
	defer jobsMutex.Unlock()
	triggeredJobs = append(triggeredJobs, job)
	log.Printf("Triggered job added: %+v\n", job)
}

// getStoredJobs returns the global slice of triggered jobs
func getStoredJobs() []triggeredJob {
	jobsMutex.Lock()
	defer jobsMutex.Unlock()
	return triggeredJobs
}

// getTriggeredJobs returns the slice of triggered jobs
func getTriggeredJobs(w http.ResponseWriter, r *http.Request) {
	storedJobs := getStoredJobs()
	responseJSON, err := json.Marshal(storedJobs)
	if err != nil {
		http.Error(w, fmt.Sprintf("error encoding JSON: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(responseJSON)
	if err != nil {
		log.Printf("failed to write responseJSON: %s", err)
	}
}

// deleteJobHandler is to delete a job with the Daprd sidecar
func deleteJobHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the job name from the URL path parameters
	vars := mux.Vars(r)
	jobName := vars["name"]

	log.Printf("Deleting job named: %s", jobName)
	_, statusCode, err := makeHTTPCall(jobName, nil, http.MethodDelete)
	if err != nil {
		http.Error(w, fmt.Sprintf("error scheduling job: %v", err), statusCode)
		return
	}
	w.WriteHeader(statusCode)
}

// getJobHandler is to get a job from the Daprd sidecar
func getJobHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the job name from the URL path parameters
	vars := mux.Vars(r)
	jobName := vars["name"]

	log.Printf("Getting job named: %s", jobName)
	responseBody, statusCode, err := makeHTTPCall(jobName, nil, http.MethodGet)
	if err != nil {
		http.Error(w, fmt.Sprintf("error scheduling job: %v", err), statusCode)
		return
	}

	// Write the response body
	if statusCode == http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, err := w.Write(responseBody)
		if err != nil {
			log.Printf("failed to write response body: %s", err)
		}
	} else {
		w.WriteHeader(statusCode)
	}
}

// jobHandler is to receive the job at trigger time
func jobHandler(w http.ResponseWriter, r *http.Request) {
	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("error reading request body: %v", err), http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var tjob triggeredJob
	if err := json.Unmarshal(reqBody, &tjob); err != nil {
		http.Error(w, fmt.Sprintf("error decoding JSON: %v", err), http.StatusBadRequest)
		return
	}
	vars := mux.Vars(r)
	jobName := vars["name"]
	log.Printf("Adding job to global slice: %s", jobName)

	addTriggeredJob(tjob)
	w.WriteHeader(http.StatusOK)
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	// schedule the job to the daprd sidecar
	router.HandleFunc("/scheduleJob/{name}", scheduleJobHandler).Methods(http.MethodPost)
	// get the scheduled job from the daprd sidecar
	router.HandleFunc("/getJob/{name}", getJobHandler).Methods(http.MethodGet)
	// delete the job from the daprd sidecar
	router.HandleFunc("/deleteJob/{name}", deleteJobHandler).Methods(http.MethodDelete)

	// receive triggered job from daprd sidecar
	router.HandleFunc("/job/{name}", jobHandler).Methods(http.MethodPost)
	// get the triggered jobs back for testing purposes
	router.HandleFunc("/getTriggeredJobs", getTriggeredJobs).Methods(http.MethodGet)

	router.HandleFunc("/healthz", healthzHandler).Methods(http.MethodGet)
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Scheduler app listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
