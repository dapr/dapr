/*
Copyright 2024 The Dapr Authors
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

package subscriber

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

type Option func(*options)

type RouteEvent struct {
	Route string
	*event.Event
}

type PublishRequest struct {
	Daprd           *daprd.Daprd
	PubSubName      string
	Topic           string
	Data            string
	DataContentType *string
}

type PublishBulkRequestEntry struct {
	EntryID     string `json:"entryId"`
	Event       string `json:"event"`
	ContentType string `json:"contentType,omitempty"`
}

type PublishBulkRequest struct {
	Daprd      *daprd.Daprd
	PubSubName string
	Topic      string
	Entries    []PublishBulkRequestEntry
}

type Subscriber struct {
	app     *app.App
	client  *http.Client
	inCh    chan *RouteEvent
	inBulk  chan *pubsub.BulkSubscribeEnvelope
	closeCh chan struct{}
}

func New(t *testing.T, fopts ...Option) *Subscriber {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	inCh := make(chan *RouteEvent, 100)
	inBulk := make(chan *pubsub.BulkSubscribeEnvelope, 100)
	closeCh := make(chan struct{})

	appOpts := make([]app.Option, 0, len(opts.routes)+len(opts.bulkRoutes)+len(opts.handlerFuncs))
	for _, route := range opts.routes {
		appOpts = append(appOpts, app.WithHandlerFunc(route, func(w http.ResponseWriter, r *http.Request) {
			var ce event.Event
			require.NoError(t, json.NewDecoder(r.Body).Decode(&ce))
			select {
			case inCh <- &RouteEvent{Route: r.URL.Path, Event: &ce}:
			case <-closeCh:
			case <-r.Context().Done():
			}
		}))
	}
	for _, route := range opts.bulkRoutes {
		appOpts = append(appOpts, app.WithHandlerFunc(route, func(w http.ResponseWriter, r *http.Request) {
			var ce pubsub.BulkSubscribeEnvelope
			require.NoError(t, json.NewDecoder(r.Body).Decode(&ce))
			select {
			case inBulk <- &ce:
			case <-closeCh:
			case <-r.Context().Done():
			}

			type statusT struct {
				EntryID string `json:"entryId"`
				Status  string `json:"status"`
			}
			type respT struct {
				Statuses []statusT `json:"statuses"`
			}

			var resp respT
			for _, entry := range ce.Entries {
				resp.Statuses = append(resp.Statuses, statusT{EntryID: entry.EntryId, Status: "SUCCESS"})
			}
			json.NewEncoder(w).Encode(resp)
		}))
	}

	appOpts = append(appOpts, opts.handlerFuncs...)

	return &Subscriber{
		app:     app.New(t, appOpts...),
		client:  util.HTTPClient(t),
		inCh:    inCh,
		inBulk:  inBulk,
		closeCh: closeCh,
	}
}

func (s *Subscriber) Run(t *testing.T, ctx context.Context) {
	t.Helper()
	s.app.Run(t, ctx)
}

func (s *Subscriber) Cleanup(t *testing.T) {
	t.Helper()
	close(s.closeCh)
	s.app.Cleanup(t)
}

func (s *Subscriber) Port() int {
	return s.app.Port()
}

func (s *Subscriber) Receive(t *testing.T, ctx context.Context) *RouteEvent {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		require.Fail(t, "timed out waiting for event response")
		return nil
	case in := <-s.inCh:
		return in
	}
}

func (s *Subscriber) ReceiveBulk(t *testing.T, ctx context.Context) *pubsub.BulkSubscribeEnvelope {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		require.Fail(t, "timed out waiting for event response")
		return nil
	case in := <-s.inBulk:
		return in
	}
}

func (s *Subscriber) AssertEventChanLen(t *testing.T, l int) {
	t.Helper()
	assert.Len(t, s.inCh, l)
}

func (s *Subscriber) ExpectPublishReceive(t *testing.T, ctx context.Context, req PublishRequest) {
	t.Helper()

	s.Publish(t, ctx, req)
	s.Receive(t, ctx)
	s.AssertEventChanLen(t, 0)
}

func (s *Subscriber) ExpectPublishError(t *testing.T, ctx context.Context, req PublishRequest) {
	t.Helper()
	//nolint:bodyclose
	resp := s.publish(t, ctx, req)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	s.AssertEventChanLen(t, 0)
}

func (s *Subscriber) ExpectPublishNoReceive(t *testing.T, ctx context.Context, req PublishRequest) {
	t.Helper()
	s.Publish(t, ctx, req)
	s.AssertEventChanLen(t, 0)
}

func (s *Subscriber) Publish(t *testing.T, ctx context.Context, req PublishRequest) {
	t.Helper()
	//nolint:bodyclose
	resp := s.publish(t, ctx, req)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func (s *Subscriber) PublishBulk(t *testing.T, ctx context.Context, req PublishBulkRequest) {
	t.Helper()
	//nolint:bodyclose
	resp := s.publishBulk(t, ctx, req)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func (s *Subscriber) publish(t *testing.T, ctx context.Context, req PublishRequest) *http.Response {
	t.Helper()
	reqURL := fmt.Sprintf("http://%s/v1.0/publish/%s/%s", req.Daprd.HTTPAddress(), req.PubSubName, req.Topic)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(req.Data))
	require.NoError(t, err)
	if req.DataContentType != nil {
		hreq.Header.Add("Content-Type", *req.DataContentType)
	}
	resp, err := s.client.Do(hreq)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp
}

func (s *Subscriber) publishBulk(t *testing.T, ctx context.Context, req PublishBulkRequest) *http.Response {
	t.Helper()

	payload, err := json.Marshal(req.Entries)
	require.NoError(t, err)
	reqURL := fmt.Sprintf("http://%s/v1.0-alpha1/publish/bulk/%s/%s", req.Daprd.HTTPAddress(), req.PubSubName, req.Topic)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	require.NoError(t, err)
	hreq.Header.Add("Content-Type", "application/json")
	resp, err := s.client.Do(hreq)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp
}
