// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	"os"

	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
)

// testMInterfaces interface is used for testing TestRunner
type testingMInterface interface {
	Run() int
}

// TestRunner holds appmanager
type TestRunner struct {
	AppResources *TestResources
	testApps     []kube.AppDescription
}

// NewTestRunner returns TestRunner instance for e2e test
func NewTestRunner(runnerID string) *TestRunner {
	return &TestRunner{
		AppResources: new(TestResources),
	}
}

// Start is the entry point of Dapr test runner
func (tr *TestRunner) Start(m testingMInterface) {
	// TODO: KubeClient will be properly configured by go test arguments
	kubeClient, _ := kube.NewKubeClient("", "")

	// Build app resources and setup test apps
	tr.buildAppResources(kubeClient)
	tr.AppResources.Setup()

	// Executes Test* methods in *_test.go
	ret := m.Run()

	// Tearing down app resources
	tr.AppResources.TearDown()

	os.Exit(ret)
}

// RegisterTestApps adds the test apps which will be deployed before running the test
func (tr *TestRunner) RegisterTestApps(apps []kube.AppDescription) {
	tr.testApps = append(tr.testApps, apps...)
}

// buildAppResources builds TestResources for the registered test apps for k8s cluster
func (tr *TestRunner) buildAppResources(kubeClient *kube.KubeClient) {
	for _, app := range tr.testApps {
		tr.AppResources.Add(kube.NewAppManager(kubeClient, kube.DaprTestKubeNameSpace, app))
	}
}

// AcquireAppExternalURL acquires external url from k8s
func (tr *TestRunner) AcquireAppExternalURL(name string) string {
	app := tr.AppResources.FindActiveResource(name)
	return app.(*kube.AppManager).AcquireExternalURL()
}
