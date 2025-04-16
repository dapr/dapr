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
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/tests/apps/utils"
	"github.com/dapr/go-sdk/client"
)

const (
	pubsubName         = "messagebus"
	topicName          = "pubsub-topic-streaming"
	inmemoryPubsubName = "inmemorypubsub"
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
	router.HandleFunc("/tests/streaming-order-subscribe", streamingOrderSubscribeHandler).Methods("POST")
	router.HandleFunc("/tests/streaming-order-in-memory-subscribe", streamingOrderSubscribeInMemoryHandler).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func streamingOrderSubscribeHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	count := r.URL.Query().Get("count")
	numberOfMessages, err := strconv.Atoi(count)
	if err != nil {
		log.Printf("error parsing count: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	subscriptionOptions := client.SubscriptionOptions{
		PubsubName: pubsubName,
		Topic:      topicName,
	}
	sub, err := sdkClient.Subscribe(ctx, subscriptionOptions)
	if err != nil {
		log.Printf("error subscribing to pubsub %s topic %s: %v", pubsubName, topicName, err)
		return
	}

	defer sub.Close()

	receivedMessagesChan := make(chan int, numberOfMessages)
	receivedCount := atomic.Int32{}
	receivedCount.Store(0)

	for {
		msg, recvErr := sub.Receive()
		if recvErr != nil {
			log.Printf("error receiving message: %v", recvErr)
			continue
		}

		data := msg.Data.(string)
		msgId, err := strconv.Atoi(data)
		if err != nil {
			log.Printf("error converting message id to int: %v", err)
			continue
		}
		receivedMessagesChan <- msgId
		err = msg.Success()
		if err != nil {
			log.Printf("error marking message as successful: %v", err)
		}
		receivedCount.Add(1)
		if int(receivedCount.Load()) == numberOfMessages {
			log.Printf("all messages received successfully")
			break
		}
	}

	messages := make([]int, 0)
	for msg := range receivedMessagesChan {
		messages = append(messages, msg)
		if (len(messages)) == numberOfMessages {
			break
		}
	}

	close(receivedMessagesChan)

	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(messages)
	if err != nil {
		log.Printf("error writing response: %v", err)
	}
}

func streamingOrderSubscribeInMemoryHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	count := r.URL.Query().Get("count")
	numberOfMessages, err := strconv.Atoi(count)
	if err != nil {
		log.Printf("error parsing count: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	subscriptionOptions := client.SubscriptionOptions{
		PubsubName: inmemoryPubsubName,
		Topic:      topicName,
	}
	sub, err := sdkClient.Subscribe(ctx, subscriptionOptions)
	if err != nil {
		log.Printf("error subscribing to pubsub %s topic %s: %v", inmemoryPubsubName, topicName, err)
		return
	}

	defer sub.Close()

	sendCount := atomic.Int32{}
	sendCount.Store(0)
	receivedCount := atomic.Int32{}
	receivedCount.Store(0)

	sentMessagesCh := make(chan int, numberOfMessages)
	receivedMessagesCh := make(chan int, numberOfMessages)

	wg := sync.WaitGroup{}

	go func() {
		wg.Add(1)
		defer wg.Done()
		for msgId := range numberOfMessages {
			sendErr := sdkClient.PublishEvent(context.Background(), inmemoryPubsubName, topicName, []byte(strconv.Itoa(msgId)))
			if sendErr != nil {
				log.Printf("error publishing event: %v", sendErr)
				return
			}
			sendCount.Add(1)
			sentMessagesCh <- msgId
		}
		log.Println("pubsub steaming in-memory done sending")
	}()

	go func() {
		wg.Add(1)
		defer wg.Done()
		for range numberOfMessages {
			event, recvErr := sub.Receive()
			if recvErr != nil {
				log.Printf("error receiving event: %v", recvErr)
				return
			}

			data := event.Data.(string)
			msgId, err := strconv.Atoi(data)
			if err != nil {
				log.Printf("error converting string to int: %v", recvErr)
				return
			}
			recvErr = event.Success()
			if recvErr != nil {
				log.Printf("error marking message as successful: %v", recvErr)
			}
			receivedCount.Add(1)
			receivedMessagesCh <- msgId
		}
		log.Println("pubsub streaming in-memory done receiving")
	}()

	wg.Wait()

	sentMessages := make([]int, 0)
	for msg := range sentMessagesCh {
		sentMessages = append(sentMessages, msg)
		if (len(sentMessages)) == numberOfMessages {
			break
		}
	}

	receivedMessages := make([]int, 0)
	for msg := range receivedMessagesCh {
		receivedMessages = append(receivedMessages, msg)
		if (len(receivedMessages)) == numberOfMessages {
			break
		}
	}

	response := struct {
		SentCount        int32 `json:"sentCount,omitempty"`
		ReceivedCount    int32 `json:"receivedCount,omitempty"`
		SentMessages     []int `json:"sentMessages,omitempty"`
		ReceivedMessages []int `json:"receivedMessages,omitempty"`
	}{
		SentCount:        sendCount.Load(),
		ReceivedCount:    receivedCount.Load(),
		SentMessages:     sentMessages,
		ReceivedMessages: receivedMessages,
	}

	bytes, err := json.Marshal(response)
	if err != nil {
		log.Printf("error marshaling response: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(bytes)
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

	log.Printf("pubsub-subscriber-streaming - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
