/*
Copyright 2023 The Dapr Authors
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

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
)

// TODO: change to take from "github.com/dapr/dapr/tests/perf" once in repository. otherwise fails on go get step in Dockerfile.
type TestParameters struct {
	QPS               int    `json:"qps"`
	ClientConnections int    `json:"clientConnections"`
	TargetEndpoint    string `json:"targetEndpoint"`
	TestDuration      string `json:"testDuration"`
	PayloadSizeKB     int    `json:"payloadSizeKB"`
	Payload           string `json:"payload"`
	StdClient         bool   `json:"stdClient"`
	Grpc              bool   `json:"grpc"`
	Dapr              string `json:"dapr"`
}

func fortioTestHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Test execution request received")

	var testParams TestParameters
	b, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(fmt.Sprintf("error reading request body: %s", err)))
		return
	}

	err = json.Unmarshal(b, &testParams)
	if err != nil {
		w.WriteHeader(400)
		w.Write([]byte(fmt.Sprintf("error parsing test params: %s", err)))
		return
	}

	log.Println("Executing test")
	results, err := runFortioTest(testParams)
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(fmt.Sprintf("error encountered while running test: %s", err)))
		return
	}

	log.Println("Test finished")
	w.Header().Add("Content-Type", "application/json")
	w.Write(results)
}

// runFortioTest accepts a set of test parameters, runs Fortio with the configured setting and returns
// the test results in json format.
func runFortioTest(params TestParameters) ([]byte, error) {
	args := buildFortioArgs(params)
	log.Printf("Running test with params: %s", args)

	cmd := exec.Command("fortio", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		return nil, err
	}
	return os.ReadFile("result.json")
}

func buildFortioArgs(params TestParameters) []string {
	var args []string

	if len(params.Payload) > 0 {
		args = []string{
			"load",
			"-json", "result.json",
			"-content-type", "application/json",
			"-qps", strconv.Itoa(params.QPS),
			"-c", strconv.Itoa(params.ClientConnections),
			"-t", params.TestDuration,
			"-payload", params.Payload,
		}
	} else {
		args = []string{
			"load",
			"-json", "result.json",
			"-qps", strconv.Itoa(params.QPS),
			"-c", strconv.Itoa(params.ClientConnections),
			"-t", params.TestDuration,
			"-payload-size", strconv.Itoa(params.PayloadSizeKB),
		}
	}
	if params.StdClient {
		args = append(args, "-stdclient")
	}

	if params.Grpc {
		args = append(args, "-grpc")
	}
	if params.Dapr != "" {
		args = append(args, "-dapr", params.Dapr)
	}

	args = append(args, params.TargetEndpoint)
	return args
}
