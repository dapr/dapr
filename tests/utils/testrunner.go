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

// Start is entry point
func (tr *TestRunner) Start(m testingMInterface) {
	kubeClient, _ := kube.NewKubeClient("", "")

	// Build app resources
	tr.buildKubeAppResources(kubeClient)

	tr.AppResources.Init()
	ret := m.Run()
	tr.AppResources.Cleanup()

	os.Exit(ret)
}

// AddTestApps adds the test apps which will be deployed before running the test
func (tr *TestRunner) AddTestApps(apps []kube.AppDescription) {
	tr.testApps = append(tr.testApps, apps...)
}

func (tr *TestRunner) AcquireExternalURL(name string) string {
	app := tr.AppResources.FindActiveResource(name)
	return app.(*kube.AppManager).AcquireExternalURL()
}

func (tr *TestRunner) buildKubeAppResources(kubeClient *kube.KubeClient) {
	for _, app := range tr.testApps {
		tr.AppResources.Add(kube.NewAppManager(kubeClient, kube.DaprTestKubeNameSpace, app))
	}
}
