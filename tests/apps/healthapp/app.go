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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
	"github.com/dapr/kit/ptr"
)

var (
	appPort          string
	appProtocol      string
	controlPort      string
	daprPort         string
	healthCheckPlan  *healthCheck
	lastInputBinding = newCountAndLast()
	lastTopicMessage = newCountAndLast()
	lastHealthCheck  = newCountAndLast()
	ready            chan struct{}
	httpClient       = utils.NewHTTPClient()
)

const (
	invokeURL  = "http://localhost:%s/v1.0/invoke/%s/method/%s"
	publishURL = "http://localhost:%s/v1.0/publish/inmemorypubsub/mytopic"
)

func main() {
	ready = make(chan struct{})
	healthCheckPlan = newHealthCheck()

	appPort = os.Getenv("APP_PORT")
	if appPort == "" {
		appPort = "4000"
	}

	appProtocol = os.Getenv("APP_PROTOCOL")

	expectAppProtocol := os.Getenv("EXPECT_APP_PROTOCOL")
	if expectAppProtocol != "" && appProtocol != expectAppProtocol {
		log.Fatalf("Expected injected APP_PROTOCOL to be %q, but got %q", expectAppProtocol, appProtocol)
	}

	controlPort = os.Getenv("CONTROL_PORT")
	if controlPort == "" {
		controlPort = "3000"
	}

	daprPort = os.Getenv("DAPR_HTTP_PORT")

	// Start the control server
	go startControlServer()

	// Publish messages in background
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-ready
		startPublishing(ctx)
	}()

	switch appProtocol {
	case "grpc":
		log.Println("using gRPC")
		// Blocking call
		startGRPC()
	case "http":
		log.Println("using HTTP")
		// Blocking call
		startHTTP()
	case "h2c":
		log.Println("using H2C")
		// Blocking call
		startH2C()
	}

	cancel()
}

func startGRPC() {
	lis, err := net.Listen("tcp", "0.0.0.0:"+appPort)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	h := &grpcServer{}
	s := grpc.NewServer()
	runtimev1pb.RegisterAppCallbackServer(s, h)
	runtimev1pb.RegisterAppCallbackHealthCheckServer(s, h)

	// Stop the gRPC server when we get a termination signal
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGTERM, os.Interrupt)
	go func() {
		// Wait for cancelation signal
		<-stopCh
		log.Println("Shutdown signal received")
		s.GracefulStop()
	}()

	// Blocking call
	log.Printf("Health App GRPC server listening on :%s", appPort)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to start gRPC server: %v", err)
	}
	log.Println("App shut down")
}

type countAndLast struct {
	count    int64
	lastTime time.Time
	lock     *sync.Mutex
}

func newCountAndLast() *countAndLast {
	return &countAndLast{
		lock: &sync.Mutex{},
	}
}

func (c *countAndLast) Record() {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.count++
	c.lastTime = time.Now()
}

func (c *countAndLast) MarshalJSON() ([]byte, error) {
	obj := struct {
		// Total number of actions
		Count int64 `json:"count"`
		// Time since last action, in ms
		Last *int64 `json:"last"`
	}{
		Count: c.count,
	}
	if c.lastTime.Unix() > 0 {
		obj.Last = ptr.Of(time.Now().Sub(c.lastTime).Milliseconds())
	}
	return json.Marshal(obj)
}

func startControlServer() {
	// Wait until the first health probe
	log.Print("Waiting for signal to start control server…")
	<-ready

	port, _ := strconv.Atoi(controlPort)
	log.Printf("Health App control server listening on http://:%d", port)
	utils.StartServer(port, func() http.Handler {
		r := mux.NewRouter().StrictSlash(true)

		// Log requests and their processing time
		r.Use(utils.LoggerMiddleware)

		// Hello world
		r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("⚙️"))
		}).Methods("GET")

		// Get last input binding
		r.HandleFunc("/last-input-binding", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			j, _ := json.Marshal(lastInputBinding)
			w.Write(j)
		}).Methods("GET")

		// Get last topic message
		r.HandleFunc("/last-topic-message", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			j, _ := json.Marshal(lastTopicMessage)
			w.Write(j)
		}).Methods("GET")

		// Get last health check
		r.HandleFunc("/last-health-check", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			j, _ := json.Marshal(lastHealthCheck)
			w.Write(j)
		}).Methods("GET")

		// Performs a service invocation
		r.HandleFunc("/invoke/{name}/{method}", func(w http.ResponseWriter, r *http.Request) {
			vars := mux.Vars(r)
			u := fmt.Sprintf(invokeURL, daprPort, vars["name"], vars["method"])
			log.Println("Invoking URL", u)
			res, err := httpClient.Post(u, r.Header.Get("content-type"), r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("Failed to invoke method: " + err.Error()))
				return
			}
			defer res.Body.Close()

			w.WriteHeader(res.StatusCode)
			io.Copy(w, res.Body)
		}).Methods("POST")

		// Update the plan
		r.HandleFunc("/set-plan", func(w http.ResponseWriter, r *http.Request) {
			ct := r.Header.Get("content-type")
			if ct != "application/json" && !strings.HasPrefix(ct, "application/json;") {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("Invalid content type"))
				return
			}

			plan := []bool{}
			err := json.NewDecoder(r.Body).Decode(&plan)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("Failed to parse body: " + err.Error()))
				return
			}

			healthCheckPlan.SetPlan(plan)

			w.WriteHeader(http.StatusNoContent)
		}).Methods("POST", "PUT")

		return r
	}, false, false)
}

func startPublishing(ctx context.Context) {
	t := time.NewTicker(time.Second)
	var i int
	for {
		select {
		case <-ctx.Done():
			log.Println("Context done; stop publishing")
		case <-t.C:
			i++
			publishMessage(i)
		}
	}
}

func publishMessage(count int) {
	u := fmt.Sprintf(publishURL, daprPort)
	log.Println("Invoking URL", u)
	body := fmt.Sprintf(`{"orderId": "%d"}`, count)
	res, err := httpClient.Post(u, "application/json", strings.NewReader(body))
	if err != nil {
		log.Printf("Failed to publish message. Error: %v", err)
		return
	}
	// Drain before closing
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
}

func startHTTP() {
	port, _ := strconv.Atoi(appPort)
	log.Printf("Health App HTTP server listening on http://:%d", port)
	utils.StartServer(port, httpRouter, true, false)
}

func startH2C() {
	log.Printf("Health App HTTP/2 Cleartext server listening on http://:%s", appPort)

	h2s := &http2.Server{}
	srv := &http.Server{
		Addr:              ":" + appPort,
		Handler:           h2c.NewHandler(httpRouter(), h2s),
		ReadHeaderTimeout: 30 * time.Second,
	}

	// Stop the server when we get a termination signal
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGINT) //nolint:staticcheck
	go func() {
		// Wait for cancelation signal
		<-stopCh
		log.Println("Shutdown signal received")
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	// Blocking call
	err := srv.ListenAndServe()
	if err != http.ErrServerClosed {
		log.Fatalf("Failed to run server: %v", err)
	}

	log.Println("Server shut down")
}

func httpRouter() http.Handler {
	r := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	r.Use(utils.LoggerMiddleware)

	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("👋"))
	}).Methods("GET")

	r.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if ready != nil {
			close(ready)
			ready = nil
		}

		lastHealthCheck.Record()

		err := healthCheckPlan.Do()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}).Methods("GET")

	r.HandleFunc("/schedule", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Received scheduled message")
		lastInputBinding.Record()
		w.WriteHeader(http.StatusOK)
	}).Methods("POST")

	r.HandleFunc("/mytopic", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Received message on /mytopic topic")
		lastTopicMessage.Record()
		w.WriteHeader(http.StatusOK)
	}).Methods("POST")

	r.HandleFunc("/foo", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Received foo request")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("🤗"))
	}).Methods("POST")

	r.HandleFunc("/dapr/subscribe", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Received /dapr/subscribe request")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]subscription{
			{
				PubsubName: "inmemorypubsub",
				Topic:      "mytopic",
				Route:      "/mytopic",
				Metadata:   map[string]string{},
			},
		})
	}).Methods("GET")

	return r
}

type subscription struct {
	PubsubName string            `json:"pubsubname"`
	Topic      string            `json:"topic"`
	Route      string            `json:"route"`
	Metadata   map[string]string `json:"metadata"`
}

// Server for gRPC
type grpcServer struct{}

func (s *grpcServer) HealthCheck(ctx context.Context, _ *emptypb.Empty) (*runtimev1pb.HealthCheckResponse, error) {
	if ready != nil {
		close(ready)
		ready = nil
	}

	lastHealthCheck.Record()

	err := healthCheckPlan.Do()
	if err != nil {
		return nil, err
	}
	return &runtimev1pb.HealthCheckResponse{}, nil
}

func (s *grpcServer) OnInvoke(_ context.Context, in *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	if in.GetMethod() == "foo" {
		log.Println("Received method invocation: " + in.GetMethod())
		return &commonv1pb.InvokeResponse{
			Data: &anypb.Any{
				Value: []byte("🤗"),
			},
		}, nil
	}

	return nil, errors.New("unexpected method invocation: " + in.GetMethod())
}

func (s *grpcServer) ListTopicSubscriptions(_ context.Context, in *emptypb.Empty) (*runtimev1pb.ListTopicSubscriptionsResponse, error) {
	return &runtimev1pb.ListTopicSubscriptionsResponse{
		Subscriptions: []*runtimev1pb.TopicSubscription{
			{
				PubsubName: "inmemorypubsub",
				Topic:      "mytopic",
			},
		},
	}, nil
}

func (s *grpcServer) OnTopicEvent(_ context.Context, in *runtimev1pb.TopicEventRequest) (*runtimev1pb.TopicEventResponse, error) {
	if in.GetTopic() == "mytopic" {
		log.Println("Received topic event: " + in.GetTopic())
		lastTopicMessage.Record()
		return &runtimev1pb.TopicEventResponse{}, nil
	}

	return nil, errors.New("unexpected topic event: " + in.GetTopic())
}

func (s *grpcServer) ListInputBindings(_ context.Context, in *emptypb.Empty) (*runtimev1pb.ListInputBindingsResponse, error) {
	return &runtimev1pb.ListInputBindingsResponse{
		Bindings: []string{"schedule"},
	}, nil
}

func (s *grpcServer) OnBindingEvent(_ context.Context, in *runtimev1pb.BindingEventRequest) (*runtimev1pb.BindingEventResponse, error) {
	if in.GetName() == "schedule" {
		log.Println("Received binding event: " + in.GetName())
		lastInputBinding.Record()
		return &runtimev1pb.BindingEventResponse{}, nil
	}

	return nil, errors.New("unexpected binding event: " + in.GetName())
}

type healthCheck struct {
	plan  []bool
	count int
	lock  *sync.Mutex
}

func newHealthCheck() *healthCheck {
	return &healthCheck{
		lock: &sync.Mutex{},
	}
}

func (h *healthCheck) SetPlan(plan []bool) {
	h.lock.Lock()
	defer h.lock.Unlock()

	// Set the new plan and reset the counter
	h.plan = plan
	h.count = 0
}

func (h *healthCheck) Do() error {
	h.lock.Lock()
	defer h.lock.Unlock()

	success := true
	if h.count < len(h.plan) {
		success = h.plan[h.count]
		h.count++
	}

	if success {
		log.Println("Responding to health check request with success")
		return nil
	}
	log.Println("Responding to health check request with failure")
	return errors.New("simulated failure")
}
