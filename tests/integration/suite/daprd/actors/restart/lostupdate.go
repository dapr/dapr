/*
Copyright 2026 The Dapr Authors
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

package restart

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(lostupdate))
}

// lostupdate checks a stale SDK cache can't lose an update after a daprd restart.
type lostupdate struct {
	host1  *actors.Actors
	cache1 *counterApp
}

const lostupdateActors = 30

func (l *lostupdate) Setup(t *testing.T) []framework.Option {
	l.cache1 = newCounterApp(t, func() string { return l.host1.Daprd().HTTPAddress() })
	l.host1 = actors.New(t, l.cache1.options()...)

	return []framework.Option{
		framework.WithProcesses(l.host1),
	}
}

func (l *lostupdate) Run(t *testing.T, ctx context.Context) {
	l.host1.WaitUntilRunning(t, ctx)

	reqs := make([]string, lostupdateActors)
	for i := range lostupdateActors {
		reqs[i] = uuid.NewString()
		assert.Equal(t, 1, incr(t, ctx, l.host1, strconv.Itoa(i), reqs[i]))
	}
	for i := range lostupdateActors {
		assert.Equal(t, 1, incr(t, ctx, l.host1, strconv.Itoa(i), reqs[i]))
		assert.Equal(t, "1", l.cache1.stored(t, ctx, strconv.Itoa(i)))
	}

	l.host1.Daprd().Restart(t, ctx)
	l.host1.Daprd().WaitUntilRunning(t, ctx)
	l.host1.Daprd().WaitUntilActorTypeHosted(t, ctx, "abc")
	l.waitForHosts(t, ctx, 1)

	var host2 *actors.Actors
	cache2 := newCounterApp(t, func() string { return host2.Daprd().HTTPAddress() })
	host2 = actors.New(t, append(cache2.options(), actors.WithPeerActor(l.host1))...)
	t.Cleanup(func() { host2.Cleanup(t) })
	host2.Run(t, ctx)
	host2.WaitUntilRunning(t, ctx)
	host2.Daprd().WaitUntilActorTypeHosted(t, ctx, "abc")
	l.waitForHosts(t, ctx, 2)

	for i := range lostupdateActors {
		reqs[i] = uuid.NewString()
		assert.Equal(t, 2, incr(t, ctx, host2, strconv.Itoa(i), reqs[i]))
	}
	for i := range lostupdateActors {
		assert.Equal(t, 2, incr(t, ctx, l.host1, strconv.Itoa(i), reqs[i]))
		assert.Equal(t, "2", l.cache1.stored(t, ctx, strconv.Itoa(i)))
	}

	moved := cache2.servedIDs()
	require.NotEmpty(t, moved)

	host2.Cleanup(t)
	l.waitForHosts(t, ctx, 1)

	for _, id := range moved {
		assert.Equal(t, 1, l.cache1.cachedSeq(id), "actor %s", id)
	}

	for _, id := range moved {
		assert.Equal(t, 3, incr(t, ctx, l.host1, id, uuid.NewString()), "actor %s", id)
		assert.Equal(t, "3", l.cache1.stored(t, ctx, id), "actor %s", id)
	}
}

func (l *lostupdate) waitForHosts(t *testing.T, ctx context.Context, n int) {
	t.Helper()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := l.host1.PlacementTables(t, ctx).Tables["default"]
		if assert.NotNil(c, table) {
			assert.Len(c, table.Hosts, n)
		}
	}, time.Second*8, time.Millisecond*10)
}

type counterApp struct {
	daprdAddress func() string
	httpClient   *http.Client

	lock   sync.Mutex
	cache  activation.Cache
	served map[string]bool
}

func newCounterApp(t *testing.T, daprdAddress func() string) *counterApp {
	return &counterApp{
		daprdAddress: daprdAddress,
		httpClient:   client.HTTP(t),
		served:       make(map[string]bool),
	}
}

func (c *counterApp) options() []actors.Option {
	return []actors.Option{
		actors.WithActorTypes("abc"),
		actors.WithDaprdOptions(daprd.WithAppID("restart-lostupdate")),
		actors.WithHandler("/actors/abc/{id}", c.deactivate),
		actors.WithHandler("/actors/abc/{id}/method/incr", c.handleIncr),
	}
}

func (c *counterApp) deactivate(w http.ResponseWriter, r *http.Request) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if !c.cache.Delete(r.PathValue("id")) {
		w.WriteHeader(http.StatusNotFound)
	}
}

func (c *counterApp) handleIncr(w http.ResponseWriter, r *http.Request) {
	c.lock.Lock()
	defer c.lock.Unlock()

	id := r.PathValue("id")
	reqID, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	done, err := c.get(r.Context(), id, "req-"+string(reqID))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if done > 0 {
		w.Write([]byte(strconv.Itoa(done)))
		return
	}

	activationID := activation.FromHeader(r)
	seq, ok := c.cache.Get(id, activationID)
	if !ok {
		stored, err := c.get(r.Context(), id, "seq")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		seq = stored
	}
	seq++

	if err := c.save(r.Context(), id, string(reqID), seq); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	c.cache.Set(id, activationID, seq)
	c.served[id] = true
	w.Write([]byte(strconv.Itoa(seq)))
}

func (c *counterApp) get(ctx context.Context, id, key string) (int, error) {
	body, err := c.do(ctx, http.MethodGet, "/v1.0/actors/abc/"+id+"/state/"+key, "")
	if err != nil || body == "" {
		return 0, err
	}
	return strconv.Atoi(body)
}

func (c *counterApp) save(ctx context.Context, id, reqID string, seq int) error {
	v := strconv.Itoa(seq)
	_, err := c.do(ctx, http.MethodPost, "/v1.0/actors/abc/"+id+"/state",
		`[{"operation":"upsert","request":{"key":"seq","value":`+v+`}},`+
			`{"operation":"upsert","request":{"key":"req-`+reqID+`","value":`+v+`}}]`)
	return err
}

func (c *counterApp) do(ctx context.Context, method, path, body string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.daprdAddress()+path, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("%s %s: (%d) %s", method, path, resp.StatusCode, b)
	}
	return string(b), nil
}

func (c *counterApp) servedIDs() []string {
	c.lock.Lock()
	defer c.lock.Unlock()
	ids := make([]string, 0, len(c.served))
	for id := range c.served {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (c *counterApp) cachedSeq(id string) int {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.cache.Value(id)
}

func incr(t *testing.T, ctx context.Context, via *actors.Actors, id, reqID string) int {
	t.Helper()

	data := activation.Invoke(t, ctx, via.GRPCClient(t, ctx), &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   id,
		Method:    "incr",
		Data:      []byte(reqID),
	})
	seq, err := strconv.Atoi(string(data))
	require.NoError(t, err)
	return seq
}

func (c *counterApp) stored(t *testing.T, ctx context.Context, id string) string {
	t.Helper()
	body, err := c.do(ctx, http.MethodGet, "/v1.0/actors/abc/"+id+"/state/seq", "")
	require.NoError(t, err)
	return body
}
