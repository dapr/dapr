// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runner

import (
	"fmt"
	"log"
	"os"

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
	addInitApps(apps []kube.AppDescription) error
	addComponents(comps []kube.ComponentDescription) error
	addApps(apps []kube.AppDescription) error

	AcquireAppExternalURL(name string) string
	Restart(name string) error
	Scale(name string, replicas int32) error
	PortForwardToApp(appName string, targetPort ...int) ([]int, error)
}

// TestRunner holds initial test apps and testing platform instance
// maintains apps and platform for e2e test
type TestRunner struct {
	// id is test runner id which will be used for logging
	id string

	components []kube.ComponentDescription

	// Initialization apps to be deployed before the test apps
	initApps []kube.AppDescription

	// TODO: Needs to define kube.AppDescription more general struct for Dapr app
	testApps []kube.AppDescription

	// Platform is the testing platform instances
	Platform PlatformInterface
}

// NewTestRunner returns TestRunner instance for e2e test
func NewTestRunner(id string, apps []kube.AppDescription,
	comps []kube.ComponentDescription,
	initApps []kube.AppDescription) *TestRunner {
	return &TestRunner{
		id:         id,
		components: comps,
		initApps:   initApps,
		testApps:   apps,
		Platform:   NewKubeTestPlatform(),
	}
}

// Start is the entry point of Dapr test runner
func (tr *TestRunner) Start(m runnable) int {
	// TODO: Add logging and reporting initialization

	// Setup testing platform
	log.Println("Running setup")
	err := tr.Platform.setup()
	defer func() {
		log.Println("Running teardown")
		tr.tearDown()
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed Platform.setup(), %s", err.Error())
		return runnerFailExitCode
	}

	// Install init apps
	log.Println("Installing init apps")
	if err := tr.Platform.addInitApps(tr.initApps); err != nil {
		fmt.Fprintf(os.Stderr, "Failed Platform.addInitApps(), %s", err.Error())
		return runnerFailExitCode
	}

	// Install components
	log.Println("Installing components")
	if err := tr.Platform.addComponents(tr.components); err != nil {
		fmt.Fprintf(os.Stderr, "Failed Platform.addComponents(), %s", err.Error())
		return runnerFailExitCode
	}

	// Install apps
	log.Println("Installing test apps")
	if err := tr.Platform.addApps(tr.testApps); err != nil {
		fmt.Fprintf(os.Stderr, "Failed Platform.addApps(), %s", err.Error())
		return runnerFailExitCode
	}

	// Executes Test* methods in *_test.go
	log.Println("Running tests...")
	return m.Run()
}

func (tr *TestRunner) tearDown() {
	// Tearing down platform
	tr.Platform.tearDown()

	// TODO: Add the resources which will be tearing down
}
