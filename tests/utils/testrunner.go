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

// runnable is an interface to implement testing.M
type runnable interface {
	Run() int
}

// PlatformInterface defines the testing platform for test runner
type PlatformInterface interface {
	setup() error
	tearDown() error

	AcquireAppExternalURL(name string) string
	AddApps(apps []kube.AppDescription) error
	InstallApps() error
}

// TestRunner holds initial test apps and testing platform instance
// maintains apps and platform for e2e test
type TestRunner struct {
	// id is test runner id which will be used for logging
	id string
	// TODO: Needs to define kube.AppDescription more general struct for Dapr app
	initialApps []kube.AppDescription

	// Platform is the testing platform instances
	Platform PlatformInterface
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
func (tr *TestRunner) Start(m runnable) int {
	// TODO: Add logging and reporting initialization

	// Setup testing platform
	err := tr.Platform.setup()
	defer tr.tearDown()
	if err != nil {
		return runnerFailExitCode
	}

	// Install apps
	if err := tr.Platform.AddApps(tr.initialApps); err != nil {
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

	// TODO: Add the resources which will be tearing down
}
