// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package testing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"
)

// KeyValState is a key value struct for state.
type KeyValState struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

// Event is an app response event.
type Event struct {
	EventName   string        `json:"eventName,omitempty"`
	To          []string      `json:"to,omitempty"`
	Concurrency string        `json:"concurrency,omitempty"`
	CreatedAt   time.Time     `json:"createdAt,omitempty"`
	State       []KeyValState `json:"state,omitempty"`
	Data        interface{}   `json:"data,omitempty"`
}

// MockApp is a mock for an app.
type MockApp struct {
	returnBody   bool
	messageCount int
	count        int
	noprint      bool
}

// NewMockApp returns a new mocked app.
func NewMockApp(returnBody bool, messageCount int, noprint bool) *MockApp {
	ret := new(MockApp)
	ret.returnBody = returnBody
	ret.messageCount = messageCount
	ret.count = 0
	ret.noprint = noprint
	return ret
}

// Run opens a test HTTP server and echo endpoint on the mock object.
func (a *MockApp) Run(port int) {
	mux := http.NewServeMux()
	mux.HandleFunc("/httptest", a.handler)
	mux.HandleFunc("/echo", a.echoHandler)
	server := http.Server{Addr: ":" + strconv.Itoa(port), Handler: mux}
	go func() {
		server.ListenAndServe()
	}()
	fmt.Println("App is running")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	ctx, cF := context.WithTimeout(context.Background(), 5*time.Second)
	cF()
	server.Shutdown(ctx)
}

func (a *MockApp) echoHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	var buffer bytes.Buffer
	for k, p := range r.URL.Query() {
		buffer.WriteString(k + "=" + p[0] + ";")
	}
	w.Write(buffer.Bytes())
}

func (a *MockApp) handler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(200)
		w.Write(nil)
		return
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)

	var msg Event
	if err := json.Unmarshal(body, &msg); err != nil {
		log.Fatal(err)
	}
	str, _ := json.Marshal(msg.Data)
	if !a.noprint {
		fmt.Println(string(str) + "#" + time.Now().UTC().Format(time.RFC3339))
	}

	w.WriteHeader(200)
	if a.returnBody {
		w.Write(body)
	} else {
		w.Write(nil)
	}
	a.count++
	if a.messageCount > 0 && a.count >= a.messageCount {
		os.Exit(0)
	}
}
