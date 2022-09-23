package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/emptypb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
)

var (
	appPort      string
	appProtocol  string
	controlPort  string
	daprHTTPPort string
	daprGRPCPort string

	currentSubscriptions []*runtimev1pb.TopicSubscription
	messages             = newMessageTracker()
	subsLock             = &sync.Mutex{}

	daprClient     runtimev1pb.DaprClient
	daprClientConn *grpc.ClientConn
	httpClient     = utils.NewHTTPClient()
)

const (
	publishURL      = "http://localhost:%s/v1.0/publish/%s/%s"
	subscriptionURL = "http://localhost:%s/v1.0/subscriptions"
)

func main() {
	appPort = os.Getenv("APP_PORT")
	if appPort == "" {
		appPort = "4000"
	}

	appProtocol = os.Getenv("APP_PROTOCOL")

	controlPort = os.Getenv("CONTROL_PORT")
	if controlPort == "" {
		controlPort = "3000"
	}

	daprHTTPPort = os.Getenv("DAPR_HTTP_PORT")
	daprGRPCPort = os.Getenv("DAPR_GRPC_PORT")

	// Start the control server
	go startControlServer()

	if appProtocol == "grpc" {
		currentSubscriptions = []*runtimev1pb.TopicSubscription{
			{
				PubsubName: "inmemorypubsub",
				Topic:      "mytopic",
			},
		}

		initGRPCClient()

		// Blocking call
		startGRPC()

		daprClientConn.Close()
	} else {
		currentSubscriptions = []*runtimev1pb.TopicSubscription{
			{
				PubsubName: "inmemorypubsub",
				Topic:      "mytopic",
				Routes: &runtimev1pb.TopicRoutes{
					Default: "/message/mytopic",
				},
			},
		}

		// Blocking call
		startHTTP()
	}
}

func startGRPC() {
	lis, err := net.Listen("tcp", "0.0.0.0:"+appPort)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	h := &grpcServer{}
	s := grpc.NewServer()
	runtimev1pb.RegisterAppCallbackServer(s, h)

	// Stop the gRPC server when we get a termination signal
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		// Wait for cancelation signal
		<-stopCh
		log.Println("Shutdown signal received")
		s.GracefulStop()
	}()

	// Blocking call
	log.Printf("Pubsub Extra App GRPC server listening on :%s", appPort)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to start gRPC server: %v", err)
	}
	log.Println("App shut down")
}

func startHTTP() {
	port, _ := strconv.Atoi(appPort)
	log.Printf("Pubsub Extra App HTTP server listening on http://:%d", port)
	utils.StartServer(port, httpRouter, true, false)
}

func initGRPCClient() {
	addr := net.JoinHostPort("127.0.0.1", daprGRPCPort)
	log.Printf("Dapr client initializing for: %s", addr)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	var err error
	daprClientConn, err = grpc.DialContext(
		ctx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	defer cancel()
	if err != nil {
		log.Fatal(err, "Error creating connection to '%s': %v", addr, err)
	}

	daprClient = runtimev1pb.NewDaprClient(daprClientConn)
}

type messageTracker struct {
	messages map[string][]string
	lock     *sync.Mutex
}

func newMessageTracker() *messageTracker {
	return &messageTracker{
		messages: map[string][]string{},
		lock:     &sync.Mutex{},
	}
}

func (t *messageTracker) Record(topic string, message string) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if t.messages[topic] != nil {
		t.messages[topic] = append(t.messages[topic], message)
	} else {
		t.messages[topic] = []string{message}
	}
}

func (t *messageTracker) Reset() {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.messages = map[string][]string{}
}

func (t *messageTracker) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.messages)
}

func startControlServer() {
	port, _ := strconv.Atoi(controlPort)
	log.Printf("Pubsub Extra App control server listening on http://:%d", port)
	utils.StartServer(port, func() *mux.Router {
		r := mux.NewRouter().StrictSlash(true)

		// Log requests and their processing time
		r.Use(utils.LoggerMiddleware)

		// Hello world
		r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("👋"))
		}).Methods("GET")

		// Get the list of received messages
		r.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
			j, _ := json.Marshal(messages)
			w.WriteHeader(http.StatusOK)
			w.Write(j)
		}).Methods("GET")

		// Reset the list of received messages
		r.HandleFunc("/reset-messages", func(w http.ResponseWriter, r *http.Request) {
			messages.Reset()
			w.WriteHeader(http.StatusNoContent)
		}).Methods("POST")

		if appProtocol == "grpc" {
			// List topic subscriptions
			r.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
				subsLock.Lock()
				defer subsLock.Unlock()

				res, err := daprClient.ListActiveTopicSubscriptions(r.Context(), &emptypb.Empty{})
				if err != nil {
					w.WriteHeader(http.StatusServiceUnavailable)
					w.Write([]byte("Request to Dapr failed: " + err.Error()))
					return
				}

				currentSubscriptions = res.GetSubscriptions()
				m, _ := protojson.Marshal(res)
				w.WriteHeader(http.StatusOK)
				w.Write(m)
			}).Methods("GET")

			// Subscribe to a topic
			r.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
				subsLock.Lock()
				defer subsLock.Unlock()

				b, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte("Failed to read body: " + err.Error()))
					return
				}

				var req runtimev1pb.TopicSubscription
				err = protojson.Unmarshal(b, &req)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte("Failed to parse request body: " + err.Error()))
					return
				}

				res, err := daprClient.SubscribeTopic(r.Context(), &req)
				if err != nil {
					w.WriteHeader(http.StatusServiceUnavailable)
					w.Write([]byte("Request to Dapr failed: " + err.Error()))
					return
				}

				currentSubscriptions = res.GetSubscriptions()
				m, _ := protojson.Marshal(res)
				w.WriteHeader(http.StatusOK)
				w.Write(m)
			}).Methods("POST", "PUT")

			// Unsubscribe from a topic
			r.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
				subsLock.Lock()
				defer subsLock.Unlock()

				b, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte("Failed to read body: " + err.Error()))
					return
				}

				var req runtimev1pb.ActiveTopicSubscription
				err = protojson.Unmarshal(b, &req)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte("Failed to parse request body: " + err.Error()))
					return
				}

				res, err := daprClient.UnsubscribeTopic(r.Context(), &req)
				if err != nil {
					w.WriteHeader(http.StatusServiceUnavailable)
					w.Write([]byte("Request to Dapr failed: " + err.Error()))
					return
				}

				currentSubscriptions = res.GetSubscriptions()
				m, _ := protojson.Marshal(res)
				w.WriteHeader(http.StatusOK)
				w.Write(m)
			}).Methods("DELETE")
		} else {
			// Subscribe to or unsubscribe from a topic, or list topic subscriptions
			r.HandleFunc("/subscriptions", func(w http.ResponseWriter, r *http.Request) {
				subsLock.Lock()
				defer subsLock.Unlock()

				// Pass to Dapr as-is
				u := fmt.Sprintf(subscriptionURL, daprHTTPPort)
				log.Println("Invoking URL", r.Method, u)

				var reqBody io.Reader
				if r.Method != "GET" {
					reqBody = r.Body
				}
				req, err := http.NewRequestWithContext(r.Context(), r.Method, u, reqBody)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Failed to create request: " + err.Error()))
					return
				}
				if r.Method != "GET" {
					req.Header.Set("content-type", r.Header.Get("content-type"))
				}

				res, err := httpClient.Do(req)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Failed to invoke method: " + err.Error()))
					return
				}
				defer res.Body.Close()

				buf, err := io.ReadAll(res.Body)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Failed to read response body: " + err.Error()))
					return
				}

				if res.StatusCode > 399 {
					// Differentiate Dapr-returned errors
					w.WriteHeader(http.StatusServiceUnavailable)
				} else {
					w.WriteHeader(res.StatusCode)
				}
				w.Write(buf)

				var pb interface {
					protoreflect.ProtoMessage
					GetSubscriptions() []*runtimev1pb.TopicSubscription
				}
				switch r.Method {
				case "GET":
					pb = &runtimev1pb.ListTopicSubscriptionsResponse{}
				case "POST", "PUT":
					pb = &runtimev1pb.SubscribeTopicResponse{}
				case "DELETE":
					pb = &runtimev1pb.UnsubscribeTopicResponse{}
				default:
					return
				}
				err = protojson.Unmarshal(buf, pb)
				if err != nil {
					log.Printf("ERROR: failed to unmarshal response into proto object: %v", err)
					return
				}
				currentSubscriptions = pb.GetSubscriptions()
			}).Methods("GET", "POST", "PUT", "DELETE")
		}

		// Publish messages
		r.HandleFunc("/publish", func(w http.ResponseWriter, r *http.Request) {
			reqs := []json.RawMessage{}
			err := json.NewDecoder(r.Body).Decode(&reqs)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("Failed to parse request body: " + err.Error()))
				return
			}

			for _, raw := range reqs {
				var req runtimev1pb.PublishEventRequest
				err = protojson.Unmarshal(raw, &req)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Failed to parse request body: " + err.Error()))
					return
				}

				if appProtocol == "grpc" {
					_, err = daprClient.PublishEvent(r.Context(), &req)
					if err != nil {
						w.WriteHeader(http.StatusInternalServerError)
						w.Write([]byte("Failed to send message via gRPC: " + err.Error()))
						return
					}
				} else {
					err = publishMessageHTTP(req.PubsubName, req.Topic, req.Data)
					if err != nil {
						w.WriteHeader(http.StatusInternalServerError)
						w.Write([]byte("Failed to send message via HTTP: " + err.Error()))
						return
					}
				}
			}

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("👍"))
		}).Methods("POST")

		return r
	}, false, false)
}

func publishMessageHTTP(pubsub string, topic string, data []byte) error {
	u := fmt.Sprintf(publishURL, daprHTTPPort, pubsub, topic)
	log.Println("Invoking URL POST", u)
	res, err := httpClient.Post(u, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	// Drain before closing
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return nil
}

func httpRouter() *mux.Router {
	r := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	r.Use(utils.LoggerMiddleware)

	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("👋"))
	}).Methods("GET")

	r.HandleFunc("/message/{topic}", func(w http.ResponseWriter, r *http.Request) {
		topic := strings.TrimSuffix(mux.Vars(r)["topic"], "*")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("ERROR: failed to read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		content := struct {
			Data json.RawMessage
		}{}
		err = json.Unmarshal(body, &content)
		if err != nil {
			log.Printf("ERROR: failed to unmarshal body '%v': %v", string(body), err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		log.Printf("Received message on topic %s: %s", topic, string(content.Data))
		messages.Record(topic, string(content.Data))
		w.WriteHeader(http.StatusOK)
	}).Methods("POST")

	r.HandleFunc("/dapr/subscribe", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Received /dapr/subscribe request")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[`))
		for i, v := range currentSubscriptions {
			if i > 0 {
				w.Write([]byte(`,`))
			}
			b, err := protojson.Marshal(v)
			if err != nil {
				log.Printf("ERROR: failed to marshal object '%v': %v", v, err)
				return
			}
			w.Write(b)
		}
		w.Write([]byte(`]`))
	}).Methods("GET")

	return r
}

// Server for gRPC
type grpcServer struct{}

func (s *grpcServer) OnInvoke(_ context.Context, in *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	return &commonv1pb.InvokeResponse{}, nil
}

func (s *grpcServer) ListTopicSubscriptions(_ context.Context, in *emptypb.Empty) (*runtimev1pb.ListTopicSubscriptionsResponse, error) {
	return &runtimev1pb.ListTopicSubscriptionsResponse{
		Subscriptions: currentSubscriptions,
	}, nil
}

func (s *grpcServer) OnTopicEvent(_ context.Context, in *runtimev1pb.TopicEventRequest) (*runtimev1pb.TopicEventResponse, error) {
	topic := strings.TrimSuffix(in.Topic, "*")
	log.Printf("Received message on topic %s: %s", topic, string(in.Data))
	messages.Record(topic, string(in.Data))
	return &runtimev1pb.TopicEventResponse{}, nil
}

func (s *grpcServer) ListInputBindings(_ context.Context, in *emptypb.Empty) (*runtimev1pb.ListInputBindingsResponse, error) {
	return &runtimev1pb.ListInputBindingsResponse{}, nil
}

func (s *grpcServer) OnBindingEvent(_ context.Context, in *runtimev1pb.BindingEventRequest) (*runtimev1pb.BindingEventResponse, error) {
	return &runtimev1pb.BindingEventResponse{}, nil
}
