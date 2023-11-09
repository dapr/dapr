/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	fuzz "github.com/google/gofuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/validation/path"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(fuzzpubsubNoRaw))
	suite.Register(new(fuzzpubsubRaw))
}

type fuzzpubsubNoRaw struct {
	fuzzpubsub
}

type fuzzpubsubRaw struct {
	fuzzpubsub
}

func (f *fuzzpubsubNoRaw) Setup(t *testing.T) []framework.Option {
	f.fuzzpubsub = fuzzpubsub{withRaw: false}
	return f.fuzzpubsub.Setup(t)
}

func (f *fuzzpubsubNoRaw) Run(t *testing.T, ctx context.Context) {
	f.fuzzpubsub.Run(t, ctx)
}

func (f *fuzzpubsubRaw) Setup(t *testing.T) []framework.Option {
	f.fuzzpubsub = fuzzpubsub{withRaw: true}
	return f.fuzzpubsub.Setup(t)
}

func (f *fuzzpubsubRaw) Run(t *testing.T, ctx context.Context) {
	f.fuzzpubsub.Run(t, ctx)
}

// TODO: @joshvanl: add error to Dapr pubsub subscribe client to reject when
// the app returns multiple routes for the same pubsub component + topic.
type testPubSub struct {
	Name   string
	Topics []testTopic
}

type testTopic struct {
	Name    string
	Route   string
	payload string
}

type fuzzpubsub struct {
	daprd *procdaprd.Daprd

	withRaw  bool
	pubSubs  []testPubSub
	respChan map[string]chan []byte
}

type pubSubMessage struct {
	DataBase64 string `json:"data_base64"`
}

type pubSubMessageData struct {
	Data string `json:"data"`
}

func (f *fuzzpubsub) Setup(t *testing.T) []framework.Option {
	const numTests = 20
	takenNames := make(map[string]bool)

	reg, err := regexp.Compile("^([a-zA-Z].*)$")
	require.NoError(t, err)

	psNameFz := fuzz.New().Funcs(func(s *string, c fuzz.Continue) {
		for *s == "" ||
			takenNames[*s] ||
			len(path.IsValidPathSegmentName(*s)) > 0 ||
			!reg.MatchString(*s) {
			*s = c.RandString()
		}
		takenNames[*s] = true
	})
	psTopicFz := fuzz.New().Funcs(func(s *string, c fuzz.Continue) {
		for *s == "" || takenNames[*s] ||
			len(path.IsValidPathSegmentName(*s)) > 0 ||
			strings.HasPrefix(*s, "/") {
			*s = c.RandString()
		}
		takenNames[*s] = true
	})
	psRouteFz := fuzz.New().Funcs(func(s *string, c fuzz.Continue) {
		for *s == "" || *s == "/" || takenNames[*s] || strings.HasPrefix(*s, "//") ||
			strings.HasPrefix(*s, ".") || strings.Contains(*s, "?") ||
			strings.Contains(*s, "#") || strings.Contains(*s, "%") ||
			strings.Contains(*s, " ") || strings.Contains(*s, "\t") ||
			strings.Contains(*s, "\n") || strings.Contains(*s, "\r") {
			*s = "/" + c.RandString()
		}
		takenNames[*s] = true
	})

	f.pubSubs = make([]testPubSub, numTests)
	f.respChan = make(map[string]chan []byte)

	files := make([]string, numTests)
	for i := 0; i < numTests; i++ {
		psNameFz.Fuzz(&f.pubSubs[i].Name)

		topicsB, err := rand.Int(rand.Reader, big.NewInt(30))
		require.NoError(t, err)
		topics := int(topicsB.Int64() + 1)
		f.pubSubs[i].Topics = make([]testTopic, topics)
		for j := 0; j < topics; j++ {
			psTopicFz.Fuzz(&f.pubSubs[i].Topics[j].Name)
			psRouteFz.Fuzz(&f.pubSubs[i].Topics[j].Route)
			f.respChan[f.pubSubs[i].Topics[j].Route] = make(chan []byte, 0)
			fuzz.New().Fuzz(&f.pubSubs[i].Topics[j].payload)
		}

		files[i] = fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: '%s'
spec:
  type: pubsub.in-memory
  version: v1
`,
			// Escape single quotes in the store name.
			strings.ReplaceAll(f.pubSubs[i].Name, "'", "''"))
	}

	var topicSubs []string
	handler := http.NewServeMux()
	for _, sub := range f.pubSubs {
		pubsubName, err := json.Marshal(sub.Name)
		require.NoError(t, err)
		for _, topic := range sub.Topics {
			topicName, err := json.Marshal(topic.Name)
			require.NoError(t, err)
			route, err := json.Marshal(topic.Route)
			require.NoError(t, err)
			if f.withRaw {
				topicSubs = append(topicSubs, fmt.Sprintf(`{
  "pubsubname": %s,
  "topic": %s,
  "route": %s,
  "metadata": {
    "rawPayload": "true"
  }
}`, pubsubName, topicName, route))
			} else {
				topicSubs = append(topicSubs, fmt.Sprintf(`{
  "pubsubname": %s,
  "topic": %s,
  "route": %s
}`, pubsubName, topicName, route))
			}

			handler.HandleFunc(topic.Route, func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				f.respChan[r.URL.Path] <- body
			})
		}
	}

	handler.HandleFunc("/dapr/subscribe", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[" + strings.Join(topicSubs, ",") + "]"))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	f.daprd = procdaprd.New(t, procdaprd.WithAppPort(srv.Port()), procdaprd.WithResourceFiles(files...))

	return []framework.Option{
		framework.WithProcesses(f.daprd, srv),
	}
}

func (f *fuzzpubsub) Run(t *testing.T, ctx context.Context) {
	f.daprd.WaitUntilRunning(t, ctx)

	t.Skip("TODO: @joshvanl skipping until pubsub publish is made stable")

	pt := util.NewParallel(t)
	for i := range f.pubSubs {
		pubsubName := f.pubSubs[i].Name
		for j := range f.pubSubs[i].Topics {
			topicName := f.pubSubs[i].Topics[j].Name
			route := f.pubSubs[i].Topics[j].Route
			payload := f.pubSubs[i].Topics[j].payload
			pt.Add(func(col *assert.CollectT) {
				reqURL := fmt.Sprintf("http://127.0.0.1:%d/v1.0/publish/%s/%s",
					f.daprd.HTTPPort(), url.QueryEscape(pubsubName), url.QueryEscape(topicName))
				// TODO: @joshvanl: under heavy load, messages seem to get lost here
				// with no response from Dapr. Until this is fixed, we use to smaller
				// timeout, and retry on context deadline exceeded.
				for {
					reqCtx, cancel := context.WithTimeout(ctx, time.Second*5)
					t.Cleanup(cancel)

					b, err := json.Marshal(payload)
					require.NoError(col, err)

					req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, reqURL, bytes.NewReader(b))
					require.NoError(col, err)
					req.Header.Set("Content-Type", "application/json")
					resp, err := util.HTTPClient(t).Do(req)
					if errors.Is(err, context.DeadlineExceeded) {
						// Only retry if we haven't exceeded the test timeout.
						d, ok := ctx.Deadline()
						require.True(col, ok)
						require.True(col, time.Now().After(d))
						continue
					}
					require.NoError(col, err)

					assert.Equal(col, http.StatusNoContent, resp.StatusCode, reqURL)
					var respBody []byte
					respBody, err = io.ReadAll(resp.Body)
					require.NoError(col, err)
					require.NoError(col, resp.Body.Close())
					assert.Empty(col, string(respBody))
					break
				}

				select {
				case body := <-f.respChan[route]:
					var data []byte
					if f.withRaw {
						var message pubSubMessage
						require.NoError(col, json.Unmarshal(body, &message))
						var err error
						data, err = base64.StdEncoding.DecodeString(message.DataBase64)
						require.NoError(col, err)
					} else {
						data = body
					}
					var messageData pubSubMessageData
					require.NoError(col, json.Unmarshal(data, &messageData), string(data))
					assert.Equal(col, payload, messageData.Data)
				case <-ctx.Done():
					t.Fatal("timed out waiting for message")
				}
			})
		}
	}
}
