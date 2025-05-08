/*
Copyright 2025 The Dapr Authors
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
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/tests/apps/utils"
	"github.com/dapr/go-sdk/client"
)

const (
	pubsubName = "messagebus"
	topicName  = "pubsub-topic-streaming"
)

var (
	appPort      = 3000
	daprGRPCPort int
	sdkClient    client.Client
)

func init() {
	p := os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/tests/streaming-order-publish", streamingOrderPublishHandler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func streamingOrderPublishHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	count := r.URL.Query().Get("count")
	numberOfMessages, err := strconv.Atoi(count)
	if err != nil {
		log.Printf("error parsing count: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	sentMessagesChan := make(chan int, numberOfMessages)
	messageCount := atomic.Int32{}
	messageCount.Store(0)

	for {
		messageID := int(messageCount.Load())
		err := sdkClient.PublishEvent(ctx, pubsubName, topicName, []byte(strconv.Itoa(messageID)))
		if err != nil {
			log.Printf("error publishing message: %v", err)
		}
		sentMessagesChan <- messageID
		messageCount.Add(1)

		if int(messageCount.Load()) == numberOfMessages {
			log.Printf("all messages sent successfully")
			break
		}
	}

	messages := make([]int, 0)
	for msg := range sentMessagesChan {
		messages = append(messages, msg)
		if len(messages) == numberOfMessages {
			break
		}
	}

	close(sentMessagesChan)

	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(messages)
	if err != nil {
		log.Printf("error writing response: %v", err)
	}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	daprGRPCPort = utils.PortFromEnv("DAPR_GRPC_PORT", 50001)
	conn, err := grpc.DialContext(
		ctx,
		net.JoinHostPort("127.0.0.1", strconv.Itoa(daprGRPCPort)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		log.Fatalf("Error connecting to Dapr's gRPC endpoint on port %d: %v", daprGRPCPort, err)
	}

	sdkClient = client.NewClientWithConnection(conn)

	log.Printf("pubsub-publisher-streaming - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
