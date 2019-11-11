// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	"os"

	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
)

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
func (tr *TestRunner) Start(m testingMInterface) {
	// TODO: Add logging and reporting initialization

	// Setup testing platform
	tr.Platform.setup()

	// Install apps
	tr.Platform.AddTestApps(tr.initialApps)
	tr.Platform.InstallApps()

	// Executes Test* methods in *_test.go
	ret := m.Run()

	// Tearing down platform
	tr.Platform.tearDown()

	os.Exit(ret)
}
