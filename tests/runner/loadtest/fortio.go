/*
Copyright 2022 The Dapr Authors
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

package loadtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/dapr/dapr/tests/perf"
	"github.com/dapr/dapr/tests/perf/utils"
	"github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

// Fortio is used for executing tests using the fortio load testing tool.
type Fortio struct {
	testApp         string
	affinityLabel   string
	testerImage     string
	numHealthChecks int
	params          perf.TestParameters
	result          []byte
	setupOnce       *sync.Once
	testerAppURL    string
}

// Result get the load test result.
func (f *Fortio) Result() []byte {
	return f.result
}

func (f *Fortio) setup(platform runner.PlatformInterface) error {
	affinityLabels := map[string]string{}
	if f.affinityLabel != "" {
		affinityLabels["daprtest"] = f.affinityLabel
	}
	err := platform.AddApps([]kubernetes.AppDescription{{
		AppName:           f.testApp,
		DaprEnabled:       true,
		ImageName:         f.testerImage,
		Replicas:          1,
		IngressEnabled:    true,
		MetricsEnabled:    true,
		AppPort:           3001,
		DaprCPULimit:      "4.0",
		DaprCPURequest:    "0.1",
		DaprMemoryLimit:   "512Mi",
		DaprMemoryRequest: "250Mi",
		AppCPULimit:       "4.0",
		AppCPURequest:     "0.1",
		AppMemoryLimit:    "800Mi",
		AppMemoryRequest:  "2500Mi",
		Labels: map[string]string{
			"daprtest": f.testApp,
		},
		PodAffinityLabels: affinityLabels,
	}})
	if err != nil {
		return err
	}

	f.testerAppURL = platform.AcquireAppExternalURL(f.testApp)
	if err = f.validate(); err != nil {
		return err
	}

	_, err = utils.HTTPGetNTimes(f.testerAppURL, f.numHealthChecks)
	return err
}

func (f *Fortio) validate() error {
	if f.testerAppURL == "" {
		return errors.New("tester app external URL must not be empty")
	}
	return nil
}

func (f *Fortio) Run(platform runner.PlatformInterface) error {
	var err error

	// this test is reusable.
	f.setupOnce.Do(func() {
		err = f.setup(platform)
	})

	if err != nil {
		return err
	}

	if err = f.validate(); err != nil {
		return err
	}

	body, err := json.Marshal(&f.params)
	if err != nil {
		return err
	}

	daprResp, err := utils.HTTPPost(fmt.Sprintf("%s/test", f.testerAppURL), body)
	if err != nil {
		return err
	}
	f.result = daprResp
	return nil
}

// SetParams set test parameters.
func (f *Fortio) SetParams(params perf.TestParameters) *Fortio {
	f.params = params
	f.testerAppURL = ""
	f.result = nil
	return f
}

type Opt = func(*Fortio)

// WithTestAppName sets the app name to be used.
func WithTestAppName(testAppName string) Opt {
	return func(f *Fortio) {
		f.testApp = testAppName
	}
}

// WithNumHealthChecks set the number of initial healthchecks that should be made before executing the test.
func WithNumHealthChecks(hc int) Opt {
	return func(f *Fortio) {
		f.numHealthChecks = hc
	}
}

// WithParams set the test parameters.
func WithParams(params perf.TestParameters) Opt {
	return func(f *Fortio) {
		f.params = params
	}
}

// WithTesterImage sets the tester image (defaults to perf-tester).
func WithTesterImage(image string) Opt {
	return func(f *Fortio) {
		f.testerImage = image
	}
}

// WithAffinity sets the pod affinity for the fortio pod.
func WithAffinity(affinityLabel string) Opt {
	return func(f *Fortio) {
		f.affinityLabel = affinityLabel
	}
}

// NewFortio returns a fortio load tester is on given options.
func NewFortio(options ...Opt) *Fortio {
	fortioTester := &Fortio{
		testApp:         "tester",
		testerImage:     "perf-tester",
		numHealthChecks: 60,
		params:          perf.TestParameters{},
		setupOnce:       &sync.Once{},
	}

	for _, opt := range options {
		opt(fortioTester)
	}

	return fortioTester
}
