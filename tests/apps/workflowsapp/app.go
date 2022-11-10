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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/tests/apps/utils"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

var appPort = 3000

func init() {
	p := os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
}

type testCommandRequest struct {
	Message string `json:"message,omitempty"`
}

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

var httpClient = utils.NewHTTPClient()

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// testHandler is the handler for end-to-end test entry point
// test driver code call this endpoint to trigger the test
func testHandler(w http.ResponseWriter, r *http.Request) {
	testCommand := mux.Vars(r)["test"]

	// Retrieve request body contents
	var commandBody testCommandRequest
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	// Trigger the test
	res := appResponse{Message: fmt.Sprintf("%s is not supported", testCommand)}
	statusCode := http.StatusBadRequest

	startTime := epoch()
	statusCode, res = startTest(commandBody)
	res.StartTime = startTime
	res.EndTime = epoch()

	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(res)
}

func startTest(commandRequest testCommandRequest) (int, appResponse) {
	log.Printf("StartTest - message: %s", commandRequest.Message)

	// Create json payload to send over HTTP to start workflow by providing task_queue
	var jsonData = []byte(`{
		"workflow_options" :
		{
			"task_queue" : "e2e_test_queue"
		}
	}`)
	res, err := httpClient.Post("http://localhost:3500/v1.0-alpha1/workflows/temporal/HelloDaprWF/WorkflowID/start", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return http.StatusInternalServerError, appResponse{Message: err.Error()}
	}
	defer res.Body.Close()

	// Get the data response from the start workflow call
	body, _ := io.ReadAll(res.Body)
	var resultData workflows.WorkflowReference
	json.Unmarshal(body, &resultData)
	time.Sleep(2 * time.Second)

	// USe the data that was retrieved back from the start workflow call (InstanceID) to get info on the workflow
	res, err = httpClient.Get("http://localhost:3500/v1.0-alpha1/workflows/temporal/HelloDaprWF/" + string(resultData.InstanceID) + "")
	if err != nil {
		return http.StatusInternalServerError, appResponse{Message: err.Error()}
	}
	defer res.Body.Close()

	// Parse the data from the Get call, specifically status and return it. It should be complete
	body, _ = io.ReadAll(res.Body)
	var stateData workflows.StateResponse
	json.Unmarshal(body, &stateData)
	return http.StatusOK, appResponse{Message: string(stateData.Metadata["status"])}
}

// epoch returns the current unix epoch timestamp
func epoch() int {
	return int(time.Now().UnixMilli())
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/tests/{test}", testHandler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Workflow Test - listening on http://localhost:%d", appPort)

	// Create the client for the worker
	cOptions := client.Options{
		HostPort:  "dapr-temporal-frontend.dapr-tests.svc.cluster.local:7233",
		Namespace: "dapr-tests",
	}
	c, err := client.Dial(cOptions)
	if err != nil {
		log.Fatalln("unable to create Temporal client", err)
	}
	defer c.Close()

	// Start the worker and register activities
	w := worker.New(c, "e2e_test_queue", worker.Options{})
	w.RegisterWorkflow(HelloDaprWF)
	w.RegisterActivity(HelloDaprAct)

	// Start listening to the Task Queue
	log.Println("e2e worker created")
	w.Start()

	utils.StartServer(appPort, appRouter, true, false)
}

func HelloDaprWF(ctx workflow.Context) (string, error) {
	fmt.Println("Starting WF Activity")

	options := workflow.ActivityOptions{
		TaskQueue:              "e2e_test_queue",
		ScheduleToCloseTimeout: time.Second * 600,
		ScheduleToStartTimeout: time.Second * 600,
		StartToCloseTimeout:    time.Second * 600,
		HeartbeatTimeout:       time.Second * 2,
		WaitForCancellation:    true,
	}

	ctx = workflow.WithActivityOptions(ctx, options)

	var resp string
	err := workflow.ExecuteActivity(ctx, HelloDaprAct).Get(ctx, &resp)
	if err != nil {
		fmt.Println("ERROR During activity call: ", err.Error())
	}

	fmt.Println("WF Activity Finished")
	return resp, nil
}

func HelloDaprAct(ctx context.Context) (result string, err error) {
	return "Hello Dapr", nil
}
