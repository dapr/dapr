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
	"fmt"
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
var messagesCh = make(chan int, 10)

// notifyCh is used to notify completion of receiving messages
var notifyCh = make(chan struct{})

func testHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("testHandler")
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("error upgrading websocket: %s", err)
		return
	}
	defer ws.Close()

	fmt.Printf("waiting for %d messages\n", numMessages)
	<-notifyCh
	fmt.Printf("received %d messages\n", numMessages)

	err = ws.WriteMessage(websocket.TextMessage, []byte("true"))
	if err != nil {
		log.Printf("error writing message: %s", err)
	}
}

func main() {
	fmt.Println("starting app on port 3000")

	go func() {
		total := 0
		for {
			count := <-messagesCh
			total += count
			if total >= numMessages {
				notifyCh <- struct{}{}
				total -= numMessages
			}
		}
	}()

	http.HandleFunc("/dapr/subscribe", subscribeHandler)
	http.HandleFunc("/"+route, messageHandler)
	http.HandleFunc("/test", testHandler)
	log.Fatal(http.ListenAndServe(":3000", nil))
}
