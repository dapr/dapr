# Actions and Go

The following example shows a simple Go app that does the following:

1. Listens for events from an Event Source named MyEventSource
2. Publishes an event to another action named action-2
3. Registers to get all saved states when the process launches

```
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
)

var counter int

type incomingEvent struct {
	Data interface{} `json:"data"`
}

type actionEvent struct {
	To   []string    `json:"to"`
	Data interface{} `json:"data"`
}

func RestoreState(w http.ResponseWriter, r *http.Request) {
	var state map[string]interface{}
	decoder := json.NewDecoder(r.Body)
	decoder.Decode(&state)

	counter = state["counter"].(int)

	w.WriteHeader(200)
	w.Write([]byte("{}"))
}

func OnMyEventSource(w http.ResponseWriter, r *http.Request) {
	var event incomingEvent
	decoder := json.NewDecoder(r.Body)
	decoder.Decode(&event)

	fmt.Println(event.Data)

	// Publish a new event to actions
	newEvent := actionEvent{
		To:   []string{"action-2"},
		Data: event.Data,
	}

	PublishEvent(newEvent)
}

func PublishEvent(event actionEvent) {
	b, _ := json.Marshal(event)
	http.DefaultClient.Post("localhost:3500/publish", "application/json", bytes.NewBuffer(b))
}

func main() {
	r := mux.NewRouter()
	// Get state when process launches
	r.HandleFunc("/state", RestoreState)
	// Listen to events from myeventsource
	r.HandleFunc("/myeventsource", OnMyEventSource)

	// Bind to a port and pass our router in
	log.Fatal(http.ListenAndServe(":8000", r))
}
```