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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
	"github.com/dapr/kit/ptr"
)

const (
	appPortHTTP  = 3000
	appPortGRPC  = 3001
	daprPortGRPC = 50001
)

// server is our user app.
type server struct{}

type JobWrapper struct {
	Job job `json:"job"`
}

type triggeredJob struct {
	TypeURL string `json:"type_url"`
	Value   string `json:"value"`
}

type jobData struct {
	DataType string `json:"@type"`
	Value    string `json:"value"`
}

type job struct {
	Name     string  `json:"name,omitempty"`
	Data     jobData `json:"data,omitempty"`
	Schedule string  `json:"schedule,omitempty"`
	Repeats  uint32  `json:"repeats,omitempty"`
	DueTime  string  `json:"dueTime,omitempty"`
	TTL      string  `json:"ttl,omitempty"`
}

type receivedJob struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Repeats  int    `json:"repeats"`
	DueTime  string `json:"dueTime"`
	Data     struct {
		Type  string `json:"@type"`
		Value struct {
			Type  string `json:"@type"`
			Value string `json:"value"`
		} `json:"value"`
	} `json:"data"`
}

var (
	daprClient runtimev1pb.DaprClient

	triggeredJobs []triggeredJob
	jobsMutex     sync.Mutex
)

func (s *server) HealthCheck(ctx context.Context, _ *emptypb.Empty) (*runtimev1pb.HealthCheckResponse, error) {
	return &runtimev1pb.HealthCheckResponse{}, nil
}

// This method gets invoked when a remote service has called the app through Dapr
// The payload carries a Method to identify the method, a set of metadata properties and an optional payload.
func (s *server) OnInvoke(ctx context.Context, request *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	return &commonv1pb.InvokeResponse{}, nil
}

func (s *server) ListTopicSubscriptions(ctx context.Context, empty *emptypb.Empty) (*runtimev1pb.ListTopicSubscriptionsResponse, error) {
	return nil, nil
}

func (s *server) OnTopicEvent(ctx context.Context, request *runtimev1pb.TopicEventRequest) (*runtimev1pb.TopicEventResponse, error) {
	return nil, nil
}

func (s *server) ListInputBindings(ctx context.Context, empty *emptypb.Empty) (*runtimev1pb.ListInputBindingsResponse, error) {
	return nil, nil
}

func (s *server) OnBindingEvent(ctx context.Context, request *runtimev1pb.BindingEventRequest) (*runtimev1pb.BindingEventResponse, error) {
	return nil, nil
}

func (s *server) OnBulkTopicEventAlpha1(ctx context.Context, request *runtimev1pb.TopicEventBulkRequest) (*runtimev1pb.TopicEventBulkResponse, error) {
	return nil, nil
}

// OnJobEvent is called by daprd once daprd receives a job from the scheduler at trigger time
func (s *server) OnJobEventAlpha1(ctx context.Context, request *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
	jobName := request.GetName()
	data := request.GetData().GetValue()

	addTriggeredJob(triggeredJob{
		TypeURL: request.GetData().GetTypeUrl(),
		Value:   string(request.GetData().GetValue()),
	})

	log.Printf("Received job event for job %s with data: %s", jobName, data)

	// Respond to Dapr with an ack that the job was processed.
	return &runtimev1pb.JobEventResponse{}, nil
}

// addTriggeredJob appends the triggered job to the global slice
func addTriggeredJob(job triggeredJob) {
	jobsMutex.Lock()
	defer jobsMutex.Unlock()
	triggeredJobs = append(triggeredJobs, job)
	log.Printf("Triggered job added: %+v\n", job)
}

func getJobGRPC(name string) (*runtimev1pb.GetJobResponse, error) {
	log.Printf("Getting job named: %s", name)

	job := &runtimev1pb.GetJobRequest{
		Name: name,
	}

	var err error
	var receivedJob *runtimev1pb.GetJobResponse
	if receivedJob, err = daprClient.GetJobAlpha1(context.Background(), job); err != nil {
		return nil, err
	}

	log.Printf("Successfully received job named: %s", receivedJob.GetJob().GetName())
	return receivedJob, err
}

// getJobHandler is to get a job from the Daprd sidecar
func getJobHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the job name from the URL path parameters
	vars := mux.Vars(r)
	jobName := vars["name"]
	log.Printf("Getting job named: %s", jobName)

	job, err := getJobGRPC(jobName)
	if err != nil {
		log.Printf("Error get job from dapr via the grpc protocol: %s", err)
		return
	}

	if job == nil || job.GetJob() == nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	jobData := job.GetJob().GetData()
	var jo triggeredJob
	if uerr := json.Unmarshal(jobData.GetValue(), &jo); uerr != nil {
		log.Printf("Error unmarshalling decoded job data: %v", uerr)
		http.Error(w, "Failed to decode job JSON", http.StatusInternalServerError)
		return
	}

	decodedbytes, derr := base64.StdEncoding.DecodeString(jo.Value)
	if derr != nil {
		log.Printf("Error decoding base64 job data: %v", derr)
		http.Error(w, "Failed to decode base64 job data", http.StatusInternalServerError)
		return
	}

	// doing this, so we can reuse test btw grpc & http, so sending back
	// values in test expected format
	rjob := &receivedJob{
		Name:     job.GetJob().GetName(),
		Schedule: job.GetJob().GetSchedule(),
		Repeats:  int(job.GetJob().GetRepeats()),
		DueTime:  job.GetJob().GetDueTime(),
		Data: struct {
			Type  string `json:"@type"`
			Value struct {
				Type  string `json:"@type"`
				Value string `json:"value"`
			} `json:"value"`
		}{
			Type: jobData.GetTypeUrl(),
			Value: struct {
				Type  string `json:"@type"`
				Value string `json:"value"`
			}{
				Type:  jo.TypeURL,
				Value: strings.TrimSpace(string(decodedbytes)),
			},
		},
	}

	respBody, merr := json.Marshal(rjob)
	if merr != nil {
		http.Error(w, fmt.Sprintf("error marshalling job response: %v", merr), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, err = w.Write(respBody)
	if err != nil {
		log.Printf("failed to write response body: %s", err)
	}
}

func scheduleJobGRPC(name string, jobWrapper JobWrapper) error {
	log.Printf("Scheduling job named: %s", name)

	stringValue := &wrapperspb.StringValue{
		Value: jobWrapper.Job.Data.Value,
	}

	dataBytes, err := proto.Marshal(stringValue)
	if err != nil {
		return fmt.Errorf("failed to marshal job data to bytes: %w", err)
	}
	jobData := &anypb.Any{
		TypeUrl: jobWrapper.Job.Data.DataType,
		Value:   dataBytes,
	}

	jbytes, err := json.Marshal(jobData)
	if err != nil {
		return fmt.Errorf("failed to marshal job data: %w", err)
	}

	job := &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     name,
			Schedule: ptr.Of(jobWrapper.Job.Schedule),
			Repeats:  ptr.Of(jobWrapper.Job.Repeats),
			DueTime:  ptr.Of(jobWrapper.Job.DueTime),
			Data:     &anypb.Any{Value: jbytes},
		},
	}

	if _, err := daprClient.ScheduleJobAlpha1(context.Background(), job); err != nil {
		return err
	}

	log.Printf("Successfully scheduled job named: %s", name)
	return nil
}

// scheduleJobHandler is to schedule a job with the Daprd sidecar
func scheduleJobHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the job name from the URL path parameters
	vars := mux.Vars(r)
	jobName := vars["name"]

	bodyBytes, rerr := io.ReadAll(r.Body)
	if rerr != nil {
		http.Error(w, fmt.Sprintf("error reading body: %v", rerr), http.StatusInternalServerError)
		return
	}
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// Extract job data from the request body
	var j job
	if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
		http.Error(w, fmt.Sprintf("error decoding JSON: %v", err), http.StatusBadRequest)
		return
	}

	jobWrapper := JobWrapper{Job: j}
	err := scheduleJobGRPC(jobName, jobWrapper)
	if err != nil {
		log.Printf("Error scheduling job to dapr via grpc protocol: %s", err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func deleteJobGRPC(name string) error {
	log.Printf("Deleting job named: %s", name)

	job := &runtimev1pb.DeleteJobRequest{
		Name: name,
	}

	if _, err := daprClient.DeleteJobAlpha1(context.Background(), job); err != nil {
		log.Printf("Error deleting job via daprs grpc protocol: %s", err)
		return err
	}
	log.Printf("Successfully deleted job named: %s", name)
	return nil
}

// deleteJobHandler is to delete a job via reaching out to the Daprd sidecar
func deleteJobHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the job name from the URL path parameters
	vars := mux.Vars(r)
	jobName := vars["name"]

	err := deleteJobGRPC(jobName)
	if err != nil {
		log.Printf("Error deleting job to dapr via grpc protocol: %s", err)
		return
	}
	w.WriteHeader(http.StatusOK)
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

// grpc & http server. http for test driver calls, grpc for calls to dapr
func main() {
	daprClient = utils.GetGRPCClient(daprPortGRPC)

	// Create a channel to handle shutdown signals
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGINT) //nolint:staticcheck

	// Create a channel to signal that the servers have been stopped
	shutdownComplete := make(chan struct{})

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", appPortHTTP),
		Handler:           appRouter(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("Scheduler http server listening on :%d", appPortHTTP)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	grpcLis, err := net.Listen("tcp", fmt.Sprintf(":%d", appPortGRPC))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	runtimev1pb.RegisterAppCallbackServer(grpcServer, &server{})
	runtimev1pb.RegisterAppCallbackHealthCheckServer(grpcServer, &server{})
	runtimev1pb.RegisterAppCallbackAlphaServer(grpcServer, &server{})

	log.Printf("Scheduler grpc server listening on :%d", appPortGRPC)

	go func() {
		if err := grpcServer.Serve(grpcLis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	// Wait for termination signal
	go func() {
		<-stopCh
		log.Println("Shutdown signal received")
		grpcServer.GracefulStop()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Fatalf("HTTP server shutdown failed: %v", err)
		}

		close(shutdownComplete)
	}()

	// Block until the shutdown is complete
	<-shutdownComplete
	log.Println("Both servers shut down")
}
