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
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

const (
	pubSubName = "kafka-messagebus"
	topic      = "perf-test"
	route      = "perf-test"
)

var numMessages = 100

var upgrader = websocket.Upgrader{}

// messagesCh contains the number of messages received
var messagesCh chan int

// notifyCh is used to notify completion of receiving messages
var notifyCh = make(chan struct{})

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

func notify(msgRecvCh chan int, notifySendCh chan struct{}) {
	total := 0
	for {
		count := <-msgRecvCh
		total += count
		if total >= numMessages {
			notifySendCh <- struct{}{}
			total -= numMessages
		}
	}
}

func main() {
	val, ok := os.LookupEnv("TEST_NUM_MESSAGES")
	if ok {
		ival, err := strconv.Atoi(val)
		if err != nil {
			log.Printf("warning: error parsing TEST_NUM_MESSAGES: %s, falling back to: %d", err, numMessages)
		} else {
			numMessages = ival
		}
	}

	messagesCh = make(chan int, numMessages)

	go notify(messagesCh, notifyCh)

	http.HandleFunc("/dapr/subscribe", subscribeHandler)
	http.HandleFunc("/"+route+"-bulk", bulkMessageHandler)
	http.HandleFunc("/"+route, messageHandler)
	http.HandleFunc("/test", testHandler)
	log.Fatal(http.ListenAndServe(":3000", nil))
}
