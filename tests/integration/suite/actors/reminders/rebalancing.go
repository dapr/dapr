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

package reminders

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(rebalancing))
}

// Number of iterations for the test
const iterations = 30

// rebalancing tests that during rebalancing, reminders do not cause actors to be activated on 2 separate hosts.
type rebalancing struct {
	daprd              [2]*daprd.Daprd
	srv                [2]*prochttp.HTTP
	handler            [2]*httpServer
	place              *placement.Placement
	placementStream    placementv1pb.Placement_ReportDaprStatusClient
	activeActors       []atomic.Bool
	doubleActivationCh chan string
}

func (i *rebalancing) Setup(t *testing.T) []framework.Option {
	i.activeActors = make([]atomic.Bool, iterations)
	i.doubleActivationCh = make(chan string)

	// Get a temporary directory where to store the SQLite DB
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-data.db")
	t.Logf("Storing database in %s", dbPath)

	// Init placement
	i.place = placement.New(t)

	// Init two instances of daprd, each with its own server
	for j := 0; j < 2; j++ {
		i.handler[j] = &httpServer{
			activeActors:       i.activeActors,
			doubleActivationCh: i.doubleActivationCh,
		}
		i.srv[j] = prochttp.New(t, prochttp.WithHandler(i.handler[j].NewHandler(j)))
		i.daprd[j] = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.sqlite
  version: v1
  metadata:
    - name: connectionString
      value: '`+dbPath+`'
    - name: busyTimeout
      value: '10s'
    - name: disableWAL
      value: 'true'
    - name: actorStateStore
      value: 'true'
`),
			daprd.WithPlacementAddresses("localhost:"+strconv.Itoa(i.place.Port())),
			daprd.WithAppPort(i.srv[j].Port()),
			// Daprd is super noisy in debug mode when connecting to placement.
			daprd.WithLogLevel("info"),
		)
	}

	return []framework.Option{
		framework.WithProcesses(i.place, i.srv[0], i.srv[1], i.daprd[0], i.daprd[1]),
	}
}

func (i *rebalancing) Run(t *testing.T, ctx context.Context) {
	i.place.WaitUntilRunning(t, ctx)

	// Wait for daprd to be ready
	for j := 0; j < 2; j++ {
		i.daprd[j].WaitUntilRunning(t, ctx)
	}
	// Wait for actors to be ready
	for j := 0; j < 2; j++ {
		i.handler[j].WaitForActorsReady(ctx)
	}

	// Establish a connection to the placement service
	placementClientReady := make(chan error)
	placementCtx, placementCancel := context.WithCancel(ctx)
	defer placementCancel()
	go i.getPlacementClient(placementCtx, placementClientReady)
	select {
	case <-ctx.Done():
		require.Fail(t, "Placement client not ready in time")
	case err := <-placementClientReady:
		require.NoError(t, err)
	}

	client := util.HTTPClient(t)

	// Do a bunch of things in parallel
	errCh := make(chan error)

	// To start, monitor for double activations
	go func() {
		errs := make([]error, 0)
		for doubleAct := range i.doubleActivationCh {
			// An empty message is a signal to stop
			if doubleAct == "" {
				break
			}
			errs = append(errs, fmt.Errorf("double activation of actor %s", doubleAct))
		}
		if len(errs) > 0 {
			errCh <- errors.Join(errs...)
		} else {
			errCh <- nil
		}
	}()

	// Schedule reminders to be executed in 0s
	for j := 0; j < iterations; j++ {
		go func(j int) {
			rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			body := `{"dueTime": "0s"}`
			daprdURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactortype/myactorid-%d/reminders/reminder%d", i.daprd[0].HTTPPort(), j, j)
			req, rErr := http.NewRequestWithContext(rctx, http.MethodPost, daprdURL, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if rErr != nil {
				errCh <- fmt.Errorf("failed scheduling reminder %d: %w", j, rErr)
				return
			}
			resp, rErr := client.Do(req)
			if rErr != nil {
				errCh <- fmt.Errorf("failed scheduling reminder %d: %w", j, rErr)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				rb, _ := io.ReadAll(resp.Body)
				errCh <- fmt.Errorf("failed scheduling reminder %d: status code is %d. Body: %s", j, resp.StatusCode, string(rb))
				return
			}
			errCh <- nil
		}(j)
	}

	// In parallel, add another node to the placement which will trigger a rebalancing
	go func() {
		rErr := i.reportStatusToPlacement(ctx, i.placementStream, []string{"myactortype"})
		if rErr != nil {
			errCh <- fmt.Errorf("failed to trigger rebalancing: %w", rErr)
		} else {
			errCh <- nil
		}
	}()

	// Also invoke the same actors using actor invocation
	for j := 0; j < iterations; j++ {
		go func(j int) {
			rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			daprdURL := fmt.Sprintf("http://localhost:%d/v1.0/actors/myactortype/myactorid-%d/method/foo", i.daprd[0].HTTPPort(), j)
			req, rErr := http.NewRequestWithContext(rctx, http.MethodPost, daprdURL, nil)
			if rErr != nil {
				errCh <- fmt.Errorf("failed invoking actor %d: %w", j, rErr)
				return
			}
			resp, rErr := client.Do(req)
			if rErr != nil {
				errCh <- fmt.Errorf("failed invoking actor %d: %w", j, rErr)
				return
			}
			defer resp.Body.Close()
			// We don't check the status code here as it could be 500 if we tried invoking the fake app
			if resp.StatusCode != http.StatusOK {
				log.Printf("MYLOG Invoking actor %d on non-existent host", j)
			}
			errCh <- nil
		}(j)
	}

	// After 2s, stop doubleActivationCh by sending an empty message
	go func() {
		<-time.After(2 * time.Second)
		i.doubleActivationCh <- ""
	}()

	// Wait for all operations to complete
	for j := 0; j < (iterations*2)+2; j++ {
		require.NoError(t, <-errCh)
	}
}

type httpServer struct {
	num                int
	actorsReady        atomic.Bool
	actorsReadyCh      chan struct{}
	activeActors       []atomic.Bool
	doubleActivationCh chan string
}

func (h *httpServer) NewHandler(num int) http.Handler {
	h.actorsReadyCh = make(chan struct{})
	h.num = num

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
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})
	r.Put("/actors/{actorType}/{actorId}/method/{methodName}", func(w http.ResponseWriter, r *http.Request) {
		// Invoke method
		actorType := chi.URLParam(r, "actorType")
		actorID := chi.URLParam(r, "actorId")
		methodName := chi.URLParam(r, "methodName")

		parts := strings.Split(actorID, "-")
		if len(parts) != 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		actorIDNum, err := strconv.Atoi(parts[1])
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if actorIDNum > len(h.activeActors) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if !h.activeActors[actorIDNum].CompareAndSwap(false, true) {
			log.Printf("BUG!!! ACTOR %s/%s IS ALREADY ACTIVE ON ANOTHER HOST", actorType, actorID)
			h.doubleActivationCh <- fmt.Sprintf("%s/%s", actorType, actorID)
		}
		log.Printf("MYLOG [%d] Invoked actor %s/%s: method %s", h.num, actorType, actorID, methodName)

		// Simulate the actor doing some work
		time.Sleep(2 * time.Second)
		h.activeActors[actorIDNum].Store(false)

		w.WriteHeader(http.StatusOK)
	})
	r.Put("/actors/{actorType}/{actorId}/method/remind/{reminderName}", func(w http.ResponseWriter, r *http.Request) {
		// Invoke reminder
		actorType := chi.URLParam(r, "actorType")
		actorID := chi.URLParam(r, "actorId")
		reminderName := chi.URLParam(r, "reminderName")

		parts := strings.Split(actorID, "-")
		if len(parts) != 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		actorIDNum, err := strconv.Atoi(parts[1])
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if actorIDNum > len(h.activeActors) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if !h.activeActors[actorIDNum].CompareAndSwap(false, true) {
			log.Printf("BUG!!! ACTOR %s/%s IS ALREADY ACTIVE ON ANOTHER HOST", actorType, actorID)
			h.doubleActivationCh <- fmt.Sprintf("%s/%s", actorType, actorID)
		}
		log.Printf("MYLOG [%d] Invoked actor %s/%s: reminder %s", h.num, actorType, actorID, reminderName)

		// Simulate the actor doing some work
		time.Sleep(2 * time.Second)
		h.activeActors[actorIDNum].Store(false)

		w.WriteHeader(http.StatusOK)
	})
	r.Delete("/actors/{actorType}/{actorId}", func(w http.ResponseWriter, r *http.Request) {
		// Deactivate actor
		actorType := chi.URLParam(r, "actorType")
		actorID := chi.URLParam(r, "actorId")
		log.Printf("MYLOG [%d] Deactivated actor %s/%s", h.num, actorType, actorID)
		w.WriteHeader(http.StatusOK)
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

func (i *rebalancing) getPlacementClient(ctx context.Context, placementClientReady chan error) {
	// Establish a connection with placement
	conn, err := grpc.DialContext(ctx, "localhost:"+strconv.Itoa(i.place.Port()),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
	)
	if err != nil {
		placementClientReady <- err
		return
	}
	defer func() { conn.Close() }()

	// Establish a stream and send the initial heartbeat, with no actors
	// We need to retry here because this will fail until the instance of placement (the only one) acquires leadership
	for j := 0; j < 4; j++ {
		client := placementv1pb.NewPlacementClient(conn)
		stream, rErr := client.ReportDaprStatus(ctx)
		if rErr != nil {
			log.Printf("Failed to connect to placement; will retry: %v", rErr)
			// Sleep before retrying
			time.Sleep(time.Second)
			continue
		}

		reportCtx, reportCancel := context.WithTimeout(ctx, time.Second)
		defer reportCancel()
		rErr = i.reportStatusToPlacement(reportCtx, stream, []string{})
		if rErr != nil {
			log.Printf("Failed to report status to placement; will retry: %v", rErr)
			stream.CloseSend()
			// Sleep before retrying
			time.Sleep(time.Second)
			continue
		}
		i.placementStream = stream
	}

	if i.placementStream == nil {
		placementClientReady <- errors.New("did not connect to placement in time")
		return
	}

	// Report that we received a rebalancing message
	placementClientReady <- nil

	// Block until context is done
	<-ctx.Done()
}

func (i *rebalancing) reportStatusToPlacement(ctx context.Context, stream placementv1pb.Placement_ReportDaprStatusClient, entities []string) error {
	err := stream.Send(&placementv1pb.Host{
		Name:     "invalidapp",
		Port:     1234,
		Entities: entities,
		Id:       "invalidapp",
	})
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	errCh := make(chan error)
	go func() {
		for {
			o, rerr := stream.Recv()
			if rerr != nil {
				errCh <- fmt.Errorf("error from placement: %w", rerr)
			}
			if o.GetOperation() == "update" {
				errCh <- nil
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err = <-errCh:
		return err
	}
}
