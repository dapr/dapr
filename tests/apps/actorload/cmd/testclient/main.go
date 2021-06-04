// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	actor_cl "actorload/pkg/actor/client"
	cl "actorload/pkg/actor/client"
	http_client "actorload/pkg/actor/client/http"

	"errors"
	"flag"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"

	"fortio.org/fortio/log"
	"fortio.org/fortio/periodic"
	"fortio.org/fortio/stats"
	"github.com/google/uuid"

	telemetry "actorload/pkg/telemetry"
)

const (
	// defaultActorType is Actor Type Name for test.
	defaultActorType  = "StateActor"
	initialStateValue = "state"
)

// actorLoadTestRunnable has test execution code and test result stats.
type actorLoadTestRunnable struct {
	periodic.RunnerResults

	client            cl.ActorClient
	currentActorIndex int

	payload []byte
	actors  []string

	testActorType   string
	testActorMethod string

	RetCodes map[int]int64
	// internal type/data
	sizes *stats.Histogram
	// exported result
	Sizes   *stats.HistogramData
	aborter *periodic.Aborter

	telemetryClient *telemetry.TelemetryClient
}

// Run is the runnable function executed by one thread.
// This iterates the preactivated actors to call each activated actor in a round-robin manner.
func (lt *actorLoadTestRunnable) Run(t int) {
	log.Debugf("Calling in %d", t)
	size := len(lt.payload)
	code := 200

	start := time.Now()

	actorID := lt.actors[lt.currentActorIndex]
	_, err := lt.client.InvokeMethod(
		lt.testActorType, actorID,
		lt.testActorMethod,
		"application/json", lt.payload)
	if err != nil {
		if actorErr, ok := err.(*http_client.DaprActorClientError); ok {
			code = actorErr.Code
		} else {
			code = 500
		}
	}

	log.Debugf("got, code: %3d, size: %d", code, size)

	elapsed := time.Since(start)

	lt.telemetryClient.RecordLoadRequestCount(lt.testActorType, actorID, elapsed, code)

	lt.RetCodes[code]++
	lt.sizes.Record(float64(size))

	// Invoke each actor in a round-robin manner
	lt.currentActorIndex = (lt.currentActorIndex + 1) % len(lt.actors)
}

type actorLoadTestOptions struct {
	periodic.RunnerOptions

	// Number of actors used for test
	NumActors int
	// The size of payload that test runner calls actor method with this payload
	WritePayloadSize int
	TestActorType    string

	// actor method that will be called during the test
	ActorMethod string
}

func generatePayload(length int) []byte {
	var chs = []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

	payload := make([]byte, length)
	for i := range payload {
		payload[i] = chs[rand.Intn(len(chs))]
	}

	return payload
}

func activateRandomActors(client actor_cl.ActorClient, actorType string, maxActor int) []string {
	var activatedActors = []string{}
	for i := 0; i < maxActor; i++ {
		actorID := strings.Replace(uuid.New().String(), "-", "", -1)
		log.Infof("Request to activate %s.%s actor", actorType, actorID)
		_, err := client.InvokeMethod(
			actorType, actorID,
			"setActorState",
			"application/json", []byte(initialStateValue))
		if err != nil {
			log.Infof("failed to activate actor - %s.%s: %q", actorType, actorID, err)
			continue
		}
		log.Infof("Completed to activate %s.%s actor", actorType, actorID)
		activatedActors = append(activatedActors, actorID)
	}

	return activatedActors
}

func startLoadTest(opt *actorLoadTestOptions, telemetryClient *telemetry.TelemetryClient) (*actorLoadTestRunnable, error) {
	client := http_client.NewClient()
	defer client.Close()

	// Wait until Dapr runtime endpoint is available.
	if err := client.WaitUntilDaprIsReady(); err != nil {
		return nil, err
	}

	// Test prep: Activate randomly generated test actors.
	// Each test runnable will invoke actor method by iterating generated
	// test actors in a round-robin manner.
	activatedActors := activateRandomActors(client, opt.TestActorType, opt.NumActors)
	activatedActorsLen := len(activatedActors)
	if activatedActorsLen == 0 {
		return nil, errors.New("no actor is activated")
	}
	log.Infof("Activated actors: %d", activatedActorsLen)

	// Generate randome payload by the given payload size.
	payload := generatePayload(opt.WritePayloadSize)
	log.Infof("Random payload: %s", payload)

	// Set up Fortio load test runner
	r := periodic.NewPeriodicRunner(&opt.RunnerOptions)
	defer r.Options().Abort()

	testRunnable := make([]actorLoadTestRunnable, opt.NumThreads)

	// Create Test runnable to store the aggregated test results from each test thread
	aggResult := actorLoadTestRunnable{
		RetCodes: map[int]int64{},
		sizes:    stats.NewHistogram(0, 100),
	}

	// Set up parallel test threads.
	for i := 0; i < opt.NumThreads; i++ {
		r.Options().Runners[i] = &testRunnable[i]
		testRunnable[i].client = http_client.NewClient()
		testRunnable[i].actors = activatedActors
		testRunnable[i].testActorType = opt.TestActorType
		testRunnable[i].testActorMethod = opt.ActorMethod
		testRunnable[i].telemetryClient = telemetryClient
		testRunnable[i].currentActorIndex = rand.Intn(activatedActorsLen)
		testRunnable[i].payload = payload
		testRunnable[i].sizes = aggResult.sizes.Clone()
		testRunnable[i].RetCodes = map[int]int64{}
	}

	// Start test
	aggResult.RunnerResults = r.Run()

	// Aggregate results from each test
	statusCodes := []int{}
	for i := 0; i < opt.NumThreads; i++ {
		testRunnable[i].client.Close()
		for k := range testRunnable[i].RetCodes {
			if _, exists := aggResult.RetCodes[k]; !exists {
				statusCodes = append(statusCodes, k)
			}
			aggResult.RetCodes[k] += testRunnable[i].RetCodes[k]
		}
		aggResult.sizes.Transfer(testRunnable[i].sizes)
	}

	// Stop test
	r.Options().ReleaseRunners()

	// Export test result
	sort.Ints(statusCodes)
	aggResultCount := float64(aggResult.DurationHistogram.Count)
	out := r.Options().Out
	fmt.Fprintf(out, "Jitter: %t\n", aggResult.Jitter)
	for _, k := range statusCodes {
		fmt.Fprintf(out, "Code %3d : %d (%.1f %%)\n", k, aggResult.RetCodes[k], 100.*float64(aggResult.RetCodes[k])/aggResultCount)
	}

	aggResult.Sizes = aggResult.sizes.Export()
	if log.LogVerbose() {
		aggResult.Sizes.Print(out, "Response Body/Total Sizes Histogram")
	} else if log.Log(log.Warning) {
		aggResult.sizes.Counter.Print(out, "Response Body/Total Sizes")
	}

	return &aggResult, nil
}

func getFlagOptions() *actorLoadTestOptions {
	qps := flag.Float64("qps", 100.0, "QPS per thread.")
	numThreads := flag.Int("c", 10, "Number of parallel simultaneous connections.")
	duration := flag.Duration("t", time.Minute*1, "How long to run the test.")
	actorType := flag.String("a", defaultActorType, "Target test actor type")
	numActors := flag.Int("numactors", 10, "Number of randomly generated actors.")
	writePayloadSize := flag.Int("s", 1024, "The size of save state value.")
	actorMethod := flag.String("m", "setActorState", "test actor method that will be called during test. e.g. nop, setActorState, getActorState")

	flag.Parse()

	return &actorLoadTestOptions{
		RunnerOptions: periodic.RunnerOptions{
			RunType:    "actor",
			QPS:        *qps,
			Duration:   *duration,
			NumThreads: *numThreads,
		},
		NumActors:        *numActors,
		WritePayloadSize: *writePayloadSize,
		TestActorType:    *actorType,
		ActorMethod:      *actorMethod,
	}
}

func main() {
	telemetry := telemetry.NewTelemetryClient()
	telemetry.Init()

	rand.Seed(time.Now().UnixNano())
	testOptions := getFlagOptions()

	log.Infof("Starting Dapr Actor Load Test.")
	log.Infof("QPS: %f, Number of Threads: %d, Number of test actors: %d",
		testOptions.RunnerOptions.QPS,
		testOptions.RunnerOptions.NumThreads,
		testOptions.NumActors)
	log.Infof("Actor type: %s", testOptions.TestActorType)
	log.Infof("Actor method: %s", testOptions.ActorMethod)
	log.Infof("Write Payload Size: %d Bytes", testOptions.WritePayloadSize)

	if _, err := startLoadTest(testOptions, telemetry); err != nil {
		log.Fatalf("Dapr Actor Load Test is failed: %q", err)
	}

	log.Infof("Dapr Actor Load Test is done")
}
