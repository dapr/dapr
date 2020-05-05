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

type PortForwarder interface {
	Connect(name string, targetPorts ...int) ([]int, error)
	Close() error
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
	PortForwardToApp(appName string, targetPort ...int) ([]int, error)
	GetPortForwarder() PortForwarder
}

type DeploymentFunc func(platform PlatformInterface) error

// TestRunner holds initial test apps and testing platform instance
// maintains apps and platform for e2e test
type TestRunner struct {
	// id is test runner id which will be used for logging
	id string

	components []kube.ComponentDescription

	// Functions to be invoked before the test app/component deployments
	preDeployFunc DeploymentFunc

	// Functions to be invoked after the test app/component deployments
	postDeployFunc DeploymentFunc

	// TODO: Needs to define kube.AppDescription more general struct for Dapr app
	initialApps []kube.AppDescription

	// Platform is the testing platform instances
	Platform PlatformInterface
}

// NewTestRunner returns TestRunner instance for e2e test
func NewTestRunner(id string, apps []kube.AppDescription,
	comps []kube.ComponentDescription,
	preDeployFunc DeploymentFunc,
	postDeployFunc DeploymentFunc) *TestRunner {
	return &TestRunner{
		id:             id,
		components:     comps,
		preDeployFunc:  preDeployFunc,
		postDeployFunc: postDeployFunc,
		initialApps:    apps,
		Platform:       NewKubeTestPlatform(),
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

	// Run optional pre-deployment function
	if tr.preDeployFunc != nil {
		log.Println("Running pre-deployment function")
		err := tr.preDeployFunc(tr.Platform)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed preDeployFunc(), %s", err.Error())
			return runnerFailExitCode
		}
	}

	// Install components
	log.Println("Installing components")
	if err := tr.Platform.addComponents(tr.components); err != nil {
		fmt.Fprintf(os.Stderr, "Failed Platform.addComponents(), %s", err.Error())
		return runnerFailExitCode
	}

	// Install apps
	log.Println("Installing apps")
	if err := tr.Platform.addApps(tr.initialApps); err != nil {
		fmt.Fprintf(os.Stderr, "Failed Platform.addApps(), %s", err.Error())
		return runnerFailExitCode
	}

	// Run optional post-deployment function
	if tr.postDeployFunc != nil {
		log.Println("Running post-deployment function")
		err := tr.postDeployFunc(tr.Platform)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed postDeployFunc(), %s", err.Error())
			return runnerFailExitCode
		}
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
