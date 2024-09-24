package invocation

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(fifo))
}

type fifo struct {
	daprd             *daprd.Daprd
	place             *placement.Placement
	receivedCalls     []string
	receivedCallsLock sync.Mutex
}

func (f *fifo) Setup(t *testing.T) []framework.Option {

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatettl
spec:
 features:
 - name: ActorStateTTL
   enabled: true
`), 0o600))

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		method := path.Base(r.URL.Path)
		f.receivedCallsLock.Lock()
		f.receivedCalls = append(f.receivedCalls, method)
		f.receivedCallsLock.Unlock()
		time.Sleep(3 * time.Millisecond)
		w.Write([]byte(method))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	f.place = placement.New(t)
	f.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithConfigs(configFile),
		daprd.WithPlacementAddresses(f.place.Address()),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(f.place, srv, f.daprd),
	}
}

// Run this test sends 50 requests to the actor, and verifies that the actor processes them in order.
// The send frequency is higher than the actor's processing time, so we can confirm that even if the
// actor is locked for a longer time, the waiting requests are processed in order.
// The test is not completely deterministic because of the goroutine scheduling, but the added
// sleep between requests should make it more stable.
func (f *fifo) Run(t *testing.T, ctx context.Context) {
	numRuns := 500
	f.place.WaitUntilRunning(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)
	s
	client := client.HTTP(t)

	daprdURL := "http://localhost:" + strconv.Itoa(f.daprd.HTTPPort())

	var wg sync.WaitGroup
	var sent []string
	var sentLock sync.Mutex
	wg.Add(numRuns)
	for i := 0; i < numRuns; i++ {
		go func(j int) {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v1.0/actors/myactortype/myactorid/method/met%d", daprdURL, j), nil)

			require.NoError(t, err)
			fmt.Println("==============")
			fmt.Println("request url: ", req.URL)

			require.EventuallyWithT(t, func(c *assert.CollectT) {
				resp, rErr := client.Do(req)
				//nolint:testifylint
				if assert.NoError(c, rErr) {
					fmt.Println("response status code: ", resp.StatusCode)
					if assert.Equal(c, http.StatusOK, resp.StatusCode) {
						sentLock.Lock()
						sent = append(sent, fmt.Sprintf("met%d", j))
						sentLock.Unlock()
					}

				} else {
					fmt.Println("response error: ", rErr)
				}

				if resp != nil && resp.Body != nil {
					defer resp.Body.Close()
					body, readErr := io.ReadAll(resp.Body) // Use io.ReadAll to read the body

					require.NoError(c, readErr)
					fmt.Println("------------------------------------")
					fmt.Printf("Response Body: %s\n", string(body)) // Print the body as a string

				}

			}, time.Second*20, time.Millisecond*10, "actor not ready")

		}(i)
		time.Sleep(1 * time.Millisecond)
	}
	wg.Wait()

	sentLock.Lock()
	fmt.Println("len of sent: ", len(sent))
	fmt.Println("len of receivedCalls: ", len(f.receivedCalls))
	require.Equal(t, len(f.receivedCalls), len(sent))
	require.Equal(t, f.receivedCalls, sent)

	sentLock.Unlock()
	//require.Fail(t, "test failed")

}
