// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runner

import (
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	log "github.com/sirupsen/logrus"
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
	addComponents(comps []kube.ComponentDescription) error
	addApps(apps []kube.AppDescription) error

	AcquireAppExternalURL(name string) string
	Restart(name string) error
	Scale(name string, replicas int32) error
}

// TestRunner holds initial test apps and testing platform instance
// maintains apps and platform for e2e test
type TestRunner struct {
	// id is test runner id which will be used for logging
	id string

	components []kube.ComponentDescription
	// TODO: Needs to define kube.AppDescription more general struct for Dapr app
	initialApps []kube.AppDescription

	// Platform is the testing platform instances
	Platform PlatformInterface
}

// NewTestRunner returns TestRunner instance for e2e test
func NewTestRunner(id string, apps []kube.AppDescription, comps []kube.ComponentDescription) *TestRunner {
	return &TestRunner{
		id:          id,
		components:  comps,
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
		log.Errorf("Failed Platform.setup(), %s", err.Error())
		return runnerFailExitCode
	}

	// install components
	if err := tr.Platform.addComponents(tr.components); err != nil {
		log.Errorf("Failed Platform.addComponents(), %s", err.Error())
		return runnerFailExitCode
	}

	// Install apps
	if err := tr.Platform.addApps(tr.initialApps); err != nil {
		log.Errorf("Failed Platform.addApps(), %s", err.Error())
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
