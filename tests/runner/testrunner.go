/*
Copyright 2021 The Dapr Authors
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

package runner

import (
	"fmt"
	"log"
	"os"

	corev1 "k8s.io/api/core/v1"

	configurationv1alpha1 "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
)

// runnerFailExitCode is the exit code when test runner setup is failed.
const runnerFailExitCode = 1

// runnable is an interface to implement testing.M.
type runnable interface {
	Run() int
}

type LoadTester interface {
	Run(platform PlatformInterface) error
}

// PlatformInterface defines the testing platform for test runner.
//
//nolint:interfacebloat
type PlatformInterface interface {
	Setup() error
	TearDown() error

	AddComponents(comps []kube.ComponentDescription) error
	AddApps(apps []kube.AppDescription) error
	AddSecrets(secrets []kube.SecretDescription) error
	AcquireAppExternalURL(name string) string
	GetAppHostDetails(name string) (string, string, error)
	Restart(name string) error
	Scale(name string, replicas int32) error
	PortForwardToApp(appName string, targetPort ...int) ([]int, error)
	SetAppEnv(appName, key, value string) error
	GetAppUsage(appName string) (*AppUsage, error)
	GetSidecarUsage(appName string) (*AppUsage, error)
	GetTotalRestarts(appname string) (int, error)
	GetConfiguration(name string) (*configurationv1alpha1.Configuration, error)
	GetService(name string) (*corev1.Service, error)
	LoadTest(loadtester LoadTester) error
}

// AppUsage holds the CPU and Memory information for the application.
type AppUsage struct {
	CPUm     int64
	MemoryMb float64
}

// TestRunner holds initial test apps and testing platform instance
// maintains apps and platform for e2e test.
type TestRunner struct {
	// id is test runner id which will be used for logging
	id string

	components []kube.ComponentDescription

	// Initialization apps to be deployed before the test apps
	initApps []kube.AppDescription

	// TODO: Needs to define kube.AppDescription more general struct for Dapr app
	testApps []kube.AppDescription

	// secrets is the list of secrets to be created in the cluster
	secrets []kube.SecretDescription

	// Platform is the testing platform instances
	Platform PlatformInterface
}

// NewTestRunner returns TestRunner instance for e2e test.
func NewTestRunner(id string, apps []kube.AppDescription,
	comps []kube.ComponentDescription,
	initApps []kube.AppDescription,
) *TestRunner {
	return &TestRunner{
		id:         id,
		components: comps,
		initApps:   initApps,
		testApps:   apps,
		Platform:   NewKubeTestPlatform(),
	}
}

func (tr *TestRunner) AddSecrets(secrets []kube.SecretDescription) {
	tr.secrets = secrets
}

// Start is the entry point of Dapr test runner.
func (tr *TestRunner) Start(m runnable) int {
	// TODO: Add logging and reporting initialization

	// Setup testing platform
	log.Println("Running setup...")
	err := tr.Platform.Setup()
	defer func() {
		log.Println("Running teardown...")
		tr.TearDown()
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed Platform.setup(), %s", err.Error())
		return runnerFailExitCode
	}

	if tr.secrets != nil && len(tr.secrets) > 0 {
		if err := tr.Platform.AddSecrets(tr.secrets); err != nil {
			fmt.Fprintf(os.Stderr, "Failed Platform.addSecrets(), %s", err.Error())
			return runnerFailExitCode
		}
	}

	// Install components.
	if tr.components != nil && len(tr.components) > 0 {
		log.Println("Installing components...")
		if err := tr.Platform.AddComponents(tr.components); err != nil {
			fmt.Fprintf(os.Stderr, "Failed Platform.addComponents(), %s", err.Error())
			return runnerFailExitCode
		}
	}

	// Install init apps. Init apps will be deployed before the main
	// test apps and can be used to initialize components and perform
	// other setup work.
	if tr.initApps != nil && len(tr.initApps) > 0 {
		log.Println("Installing init apps...")
		if err := tr.Platform.AddApps(tr.initApps); err != nil {
			fmt.Fprintf(os.Stderr, "Failed Platform.addInitApps(), %s", err.Error())
			return runnerFailExitCode
		}
	}

	// Install test apps. These are the main apps that provide the actual testing.
	if tr.testApps != nil && len(tr.testApps) > 0 {
		log.Println("Installing test apps...")
		if err := tr.Platform.AddApps(tr.testApps); err != nil {
			fmt.Fprintf(os.Stderr, "Failed Platform.addApps(), %s", err.Error())
			return runnerFailExitCode
		}
	}

	// Executes Test* methods in *_test.go
	log.Println("Running tests...")
	return m.Run()
}

func (tr *TestRunner) TearDown() {
	// Tearing down platform
	tr.Platform.TearDown()

	// TODO: Add the resources which will be tearing down
}
