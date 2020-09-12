// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	cl "actorload/actor/client"
	http_client "actorload/actor/client/http"

	"fortio.org/fortio/log"
	"fortio.org/fortio/periodic"
	"fortio.org/fortio/stats"
)

type actorLoadTestResult struct {
	periodic.RunnerResults

	client            cl.ActorClient
	currentActorIndex int

	payload []byte
	actors  []string

	RetCodes map[int]int64
	// internal type/data
	sizes *stats.Histogram
	// exported result
	Sizes   *stats.HistogramData
	aborter *periodic.Aborter
}

func (lt *actorLoadTestResult) Run(t int) {
	log.Debugf("Calling in %d", t)
	body := []byte("dummy")
	size := len(body)
	code := 200

	_, err := lt.client.InvokeMethod(
		"StateActor", lt.actors[lt.currentActorIndex],
		"setActorState",
		"application/json", lt.payload)
	if err != nil {
		if actorErr, ok := err.(*http_client.DaprActorClientError); ok {
			code = actorErr.Code
		} else {
			code = 500
		}
	}

	log.Debugf("got, code: %3d, size: %d", code, size)

	lt.RetCodes[code]++
	lt.sizes.Record(float64(size))

	// Invoke each actor in a round-robin manner
	lt.currentActorIndex = (lt.currentActorIndex + 1) % len(lt.actors)
}
