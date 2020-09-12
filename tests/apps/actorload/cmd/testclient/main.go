// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	http_client "actorload/actor/client/http"
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
)

const (
	// actorType is Actor Type Name for test.
	actorType = "StateActor"
)

type actorLoadTestOptions struct {
	periodic.RunnerOptions

	NumActors        int
	WritePayloadSize int
}

func generatePayload(length int) []byte {
	var chs = []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

	payload := make([]byte, length)
	for i := range payload {
		payload[i] = chs[rand.Intn(len(chs))]
	}

	return payload
}

func activateTestActors(maxActor int) []string {
	client := http_client.NewClient()
	defer client.Close()

	var activatedActors = []string{}
	for i := 0; i < maxActor; i++ {
		actorID := strings.Replace(uuid.New().String(), "-", "", -1)
		log.Infof("Request to activate %s.%s actor", actorType, actorID)
		_, err := client.InvokeMethod(
			actorType, actorID,
			"setActorState",
			"application/json", []byte("state"))
		if err != nil {
			log.Infof("failed to activate actor - %s.%s: %q", actorType, actorID, err)
			continue
		}
		log.Infof("Completed to activate %s.%s actor", actorType, actorID)
		activatedActors = append(activatedActors, actorID)
	}

	return activatedActors
}

func startLoadTest(opt *actorLoadTestOptions) (*actorLoadTestResult, error) {
	// Activate all test actors
	activatedActors := activateTestActors(opt.NumActors)
	activatedActorsLen := len(activatedActors)
	if activatedActorsLen == 0 {
		return nil, errors.New("no actor is activated")
	}
	log.Infof("activated actors: %d", activatedActorsLen)

	payload := generatePayload(opt.WritePayloadSize)
	log.Infof("random payload: %s", payload)

	// Setup Fortio load test runner
	r := periodic.NewPeriodicRunner(&opt.RunnerOptions)
	defer r.Options().Abort()

	actorstate := make([]actorLoadTestResult, opt.NumThreads)

	total := actorLoadTestResult{
		RetCodes: map[int]int64{},
		sizes:    stats.NewHistogram(0, 100),
	}

	for i := 0; i < opt.NumThreads; i++ {
		r.Options().Runners[i] = &actorstate[i]
		actorstate[i].client = http_client.NewClient()
		actorstate[i].actors = activatedActors
		actorstate[i].currentActorIndex = rand.Intn(activatedActorsLen)
		actorstate[i].payload = payload
		actorstate[i].sizes = total.sizes.Clone()
		actorstate[i].RetCodes = map[int]int64{}
	}

	total.RunnerResults = r.Run()

	statusCodes := []int{}
	for i := 0; i < opt.NumThreads; i++ {
		actorstate[i].client.Close()
		for k := range actorstate[i].RetCodes {
			if _, exists := total.RetCodes[k]; !exists {
				statusCodes = append(statusCodes, k)
			}
			total.RetCodes[k] += actorstate[i].RetCodes[k]
		}
		total.sizes.Transfer(actorstate[i].sizes)
	}

	// Stop test
	r.Options().ReleaseRunners()
	sort.Ints(statusCodes)
	totalCount := float64(total.DurationHistogram.Count)
	out := r.Options().Out

	// Aggregate the test results
	fmt.Fprintf(out, "Jitter: %t\n", total.Jitter)
	for _, k := range statusCodes {
		fmt.Fprintf(out, "Code %3d : %d (%.1f %%)\n", k, total.RetCodes[k], 100.*float64(total.RetCodes[k])/totalCount)
	}

	total.Sizes = total.sizes.Export()
	if log.LogVerbose() {
		total.Sizes.Print(out, "Response Body/Total Sizes Histogram")
	} else if log.Log(log.Warning) {
		total.sizes.Counter.Print(out, "Response Body/Total Sizes")
	}

	return &total, nil
}

func getFlagOptions() *actorLoadTestOptions {
	qps := flag.Float64("qps", 100.0, "QPS per thread.")
	numThreads := flag.Int("nthreads", 10, "Number of Threads.")
	numActors := flag.Int("nactors", 10, "Number of randomly generated actors.")
	writePayloadSize := flag.Int("write-payload-size", 1024, "The size of save state value")

	flag.Parse()

	return &actorLoadTestOptions{
		RunnerOptions: periodic.RunnerOptions{
			RunType:    "actor",
			QPS:        *qps,
			NumThreads: *numThreads,
		},
		NumActors:        *numActors,
		WritePayloadSize: *writePayloadSize,
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())
	testOptions := getFlagOptions()

	log.Infof("Starting Dapr Actor Load Test.")
	log.Infof("QPS: %f, Number of Threads: %d, Number of test actors: %d",
		testOptions.RunnerOptions.QPS,
		testOptions.RunnerOptions.NumThreads,
		testOptions.NumActors)
	log.Infof("Write Payload Size: %d Bytes", testOptions.WritePayloadSize)
	log.Infof("Test will start in 5 seconds.")

	time.Sleep(time.Second * 5)

	if _, err := startLoadTest(testOptions); err != nil {
		log.Fatalf("Test is failed: %q", err)
	}

	log.Infof("Test is done")
}
