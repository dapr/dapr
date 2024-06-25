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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

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
	DataType   string `json:"@type"`
	Expression string `json:"expression"`
}

type job struct {
	Name     string  `json:"name,omitempty"`
	Data     jobData `json:"data,omitempty"`
	Schedule string  `json:"schedule,omitempty"`
	Repeats  int     `json:"repeats,omitempty"`
	DueTime  string  `json:"dueTime,omitempty"`
	TTL      string  `json:"ttl,omitempty"`
}

var (
	daprClient runtimev1pb.DaprClient

	triggeredJobs []triggeredJob
	jobsMutex     sync.Mutex
)

func scheduleJobGRPC(name string, jobWrapper JobWrapper) error {
	log.Printf("Scheduling job named: %s", name)

	dataBytes, err := json.Marshal(jobWrapper.Job.Data)
	if err != nil {
		return err
	}

	job := &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     name,
			Schedule: ptr.Of(jobWrapper.Job.Schedule),
			Repeats:  ptr.Of(uint32(jobWrapper.Job.Repeats)),
			Data: &anypb.Any{
				Value: dataBytes,
			},
		},
	}

	if _, err = daprClient.ScheduleJobAlpha1(context.Background(), job); err != nil {
		return err
	}

	log.Printf("Successfully scheduled job named: %s", name)
	return nil
}

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

// scheduleJobHandler is to schedule a job with the Daprd sidecar
func scheduleJobHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the job name from the URL path parameters
	vars := mux.Vars(r)
	jobName := vars["name"]

	// Extract job data from the request body
	var jobData job
	if err := json.NewDecoder(r.Body).Decode(&jobData); err != nil {
		http.Error(w, fmt.Sprintf("error decoding JSON: %v", err), http.StatusBadRequest)
		return
	}

	jobWrapper := JobWrapper{Job: jobData}

	err := scheduleJobGRPC(jobName, jobWrapper)
	if err != nil {
		log.Printf("Error scheduling job to dapr via grpc protocol: %s", err)
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

	router.HandleFunc("/scheduleJob/{name}", scheduleJobHandler).Methods(http.MethodPost)
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
