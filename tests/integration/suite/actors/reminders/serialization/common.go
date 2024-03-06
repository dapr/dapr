/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or impliei.
See the License for the specific language governing permissions and
limitations under the License.
*/

package serialization

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func invokeActor(t *testing.T, ctx context.Context, baseURL string, client *http.Client) {
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/method/foo", nil)
		require.NoError(c, err)
		resp, rErr := client.Do(req)
		//nolint:testifylint
		if assert.NoError(c, rErr) {
			assert.NoError(c, resp.Body.Close())
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, time.Second*20, time.Millisecond*100, "actor not ready in time")
}

func storeReminder(t *testing.T, ctx context.Context, baseURL string, client *http.Client) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, baseURL+"/reminders/newreminder", strings.NewReader(`{"dueTime": "0","period": "2m"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func loadRemindersFromDB(t *testing.T, ctx context.Context, db *sql.DB) (storedVal string) {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := db.QueryRowContext(queryCtx, "SELECT value FROM state WHERE key = 'actors||myactortype'").Scan(&storedVal)
	require.NoError(t, err)
	return storedVal
}

type httpServer struct {
	actorsReady          atomic.Bool
	actorsReadyCh        chan struct{}
	remindersInvokeCount atomic.Uint32
}

func (h *httpServer) NewHandler() http.Handler {
	h.actorsReadyCh = make(chan struct{})

	r := chi.NewRouter()
	r.Get("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if h.actorsReady.CompareAndSwap(false, true) {
			close(h.actorsReadyCh)
		}
		w.WriteHeader(http.StatusOK)
	})
	r.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {})
	r.HandleFunc("/actors/myactortype/myactorid/method/remind/newreminder", func(w http.ResponseWriter, r *http.Request) {
		h.remindersInvokeCount.Add(1)
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})
	return r
}

func (h *httpServer) WaitForActorsReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.actorsReadyCh:
		return nil
	}
}
