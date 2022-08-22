/*
Copyright 2021 The Dapr Authors
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

//nolint:forbidigo
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
	//nolint:gosec
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
	w.WriteHeader(http.StatusOK)
	var buffer bytes.Buffer
	for k, p := range r.URL.Query() {
		buffer.WriteString(k + "=" + p[0] + ";")
	}
	w.Write(buffer.Bytes())
}

func (a *MockApp) handler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
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

	w.WriteHeader(http.StatusOK)
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
