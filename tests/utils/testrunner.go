// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
)

// runnerFailExitCode is the exit code when test runner setup is failed
const runnerFailExitCode = 1

// testingMInterface interface is used for testing TestRunner
type testingMInterface interface {
	Run() int
}

// testingPlatform defines the testing platform for test runner
type testingPlatform interface {
	setup() error
	tearDown() error

	AcquireAppExternalURL(name string) string
	AddTestApps(apps []kube.AppDescription) error
	InstallApps() error
}

// TestRunner holds appmanager
type TestRunner struct {
	id string
	// TODO: Needs to define kube.AppDescription more general struct for Dapr app
	initialApps []kube.AppDescription
	Platform    testingPlatform
}

// NewTestRunner returns TestRunner instance for e2e test
func NewTestRunner(id string, apps []kube.AppDescription) *TestRunner {
	return &TestRunner{
		id:          id,
		initialApps: apps,
		Platform:    NewKubeTestPlatform(),
	}
}

// Start is the entry point of Dapr test runner
func (tr *TestRunner) Start(m testingMInterface) int {
	// TODO: Add logging and reporting initialization

	// Setup testing platform
	err := tr.Platform.setup()
	defer tr.tearDown()
	if err != nil {
		return runnerFailExitCode
	}

	// Install apps
	if err := tr.Platform.AddTestApps(tr.initialApps); err != nil {
		return runnerFailExitCode
	}
	if err := tr.Platform.InstallApps(); err != nil {
		return runnerFailExitCode
	}

	// Executes Test* methods in *_test.go
	return m.Run()
}

func (tr *TestRunner) tearDown() {
	// Tearing down platform
	tr.Platform.tearDown()
}
