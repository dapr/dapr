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

	"github.com/gorilla/websocket"
)

const (
	pubSubName  = "kafka-messagebus"
	topic       = "perf-test"
	route       = "perf-test"
	numMessages = 100
)

var upgrader = websocket.Upgrader{}

// messagesCh contains the number of messages received
var messagesCh = make(chan int, 100)

// bulkMessagesCh contains the number of messages received in bulk
var bulkMessagesCh = make(chan int, 100)

// notifyCh is used to notify completion of receiving messages
var notifyCh = make(chan struct{})

// bulkNotifyCh is used to notify completion of receiving messages in bulk
var bulkNotifyCh = make(chan struct{})

func testHandler(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("error upgrading websocket: %s", err)
		return
	}
	defer ws.Close()

	_, message, err := ws.ReadMessage()
	if err != nil {
		log.Printf("error reading message: %s", err)
		return
	}

	// wait for messages to be received
	log.Printf("subscribeType: %s", message)
	if string(message) == "bulk" {
		<-bulkNotifyCh
	} else {
		<-notifyCh
	}

	err = ws.WriteMessage(websocket.TextMessage, []byte("true"))
	if err != nil {
		log.Printf("error writing message: %s", err)
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
	go notify(messagesCh, notifyCh)
	go notify(bulkMessagesCh, bulkNotifyCh)

	http.HandleFunc("/dapr/subscribe", subscribeHandler)
	http.HandleFunc("/"+route, messageHandler)
	http.HandleFunc("/"+route+"-bulk", bulkMessageHandler)
	http.HandleFunc("/test", testHandler)
	log.Fatal(http.ListenAndServe(":3000", nil))
}
