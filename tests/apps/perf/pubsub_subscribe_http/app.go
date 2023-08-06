/*
Copyright 2023 The Dapr Authors
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
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

var (
	pubSubName string
	topic      string
	route      string
)

var numMessages = 100

var upgrader = websocket.Upgrader{}

// messagesCh contains the messages received
var messagesCh chan string

// notifyCh is used to notify completion of receiving messages
var notifyCh = make(chan struct{})

// messagesMap is used to track messages received
// and only count unique messages
var messagesMap = map[string]struct{}{}

func testHandler(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("error upgrading websocket: %s", err)
		return
	}
	defer ws.Close()

	<-notifyCh

	err = ws.WriteMessage(websocket.TextMessage, []byte("true"))
	if err != nil {
		log.Printf("error writing message: %s", err)
	}

	// gracefully close the connection
	err = ws.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(4*time.Second))
	if err != nil {
		log.Printf("error closing websocket: %s", err)
	}
}

func notify(msgRecvCh chan string, notifySendCh chan struct{}) {
	total := 0
	for {
		msg := <-msgRecvCh

		// only count unique messages
		if _, present := messagesMap[msg]; !present {
			messagesMap[msg] = struct{}{}
			total++
		}

		// notify when we have received all messages
		if total >= numMessages {
			notifySendCh <- struct{}{}
			total -= numMessages
		}
	}
}

func main() {
	err := readPubsubEnvVar()
	if err != nil {
		log.Fatalf("Error reading environment variables : %s", err.Error())
		return
	}

	val, ok := os.LookupEnv("TEST_NUM_MESSAGES")
	if ok {
		ival, err := strconv.Atoi(val)
		if err != nil {
			log.Printf("WARNING: error parsing TEST_NUM_MESSAGES: %s, falling back to: %d", err, numMessages)
		} else {
			numMessages = ival
		}
	}

	messagesCh = make(chan string, numMessages)

	go notify(messagesCh, notifyCh)
	log.Printf("Env variable route is set to %s", route)

	http.HandleFunc("/dapr/subscribe", subscribeHandler)
	http.HandleFunc("/"+route+"-bulk", bulkMessageHandler)
	http.HandleFunc("/"+route, messageHandler)
	http.HandleFunc("/test", testHandler)
	log.Fatal(http.ListenAndServe(":3000", nil)) //nolint:gosec
}

func readPubsubEnvVar() error {
	pubSubName = os.Getenv("PERF_PUBSUB_HTTP_COMPONENT_NAME")
	topic = os.Getenv("PERF_PUBSUB_HTTP_TOPIC_NAME")
	route = os.Getenv("PERF_PUBSUB_HTTP_ROUTE_NAME")
	if !validateEnvVar("PERF_PUBSUB_HTTP_COMPONENT_NAME", pubSubName) {
		return errors.New("invalid PERF_PUBSUB_HTTP_COMPONENT_NAME")
	}

	if !validateEnvVar("PERF_PUBSUB_HTTP_TOPIC_NAME", topic) {
		return errors.New("invalid PERF_PUBSUB_HTTP_TOPIC_NAME")
	}

	if !validateEnvVar("PERF_PUBSUB_HTTP_ROUTE_NAME", route) {
		return errors.New("invalid PERF_PUBSUB_HTTP_ROUTE_NAME")
	}

	return nil
}

func validateEnvVar(key string, value string) bool {
	if value == "" {
		log.Printf("error: Env variable %s is set to empty", key)
		return false
	}

	log.Printf("Env variable %s is set to %s", key, value)

	return true
}
