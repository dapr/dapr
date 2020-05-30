// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runner

import (
	"fmt"
	"os"

	"log"

	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
)

const (
	defaultImageRegistry = "docker.io/dapriotest"
	defaultImageTag      = "latest"
)

// KubeTestPlatform includes K8s client for testing cluster and kubernetes testing apps.
type KubeTestPlatform struct {
	AppResources       *TestResources
	ComponentResources *TestResources
	kubeClient         *kube.KubeClient
}

// NewKubeTestPlatform creates KubeTestPlatform instance.
func NewKubeTestPlatform() *KubeTestPlatform {
	return &KubeTestPlatform{
		AppResources:       new(TestResources),
		ComponentResources: new(TestResources),
	}
}

func (c *KubeTestPlatform) setup() (err error) {
	// TODO: KubeClient will be properly configured by go test arguments
	c.kubeClient, err = kube.NewKubeClient("", "")

	return
}

func (c *KubeTestPlatform) tearDown() error {
	if err := c.AppResources.tearDown(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to tear down AppResources. got: %q", err)
	}

	if err := c.ComponentResources.tearDown(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to tear down ComponentResources. got: %q", err)
	}

	// TODO: clean up kube cluster

	return nil
}

// addComponents adds component to disposable Resource queues.
func (c *KubeTestPlatform) addComponents(comps []kube.ComponentDescription) error {
	if c.kubeClient == nil {
		return fmt.Errorf("kubernetes cluster needs to be setup")
	}

	for _, comp := range comps {
		c.ComponentResources.Add(kube.NewDaprComponent(c.kubeClient, kube.DaprTestNamespace, comp))
	}

	// setup component resources
	if err := c.ComponentResources.setup(); err != nil {
		return err
	}

	return nil
}

// addApps adds test apps to disposable App Resource queues.
func (c *KubeTestPlatform) addApps(apps []kube.AppDescription) error {
	if c.kubeClient == nil {
		return fmt.Errorf("kubernetes cluster needs to be setup before calling BuildAppResources")
	}

	for _, app := range apps {
		if app.RegistryName == "" {
			app.RegistryName = c.imageRegistry()
		}
		if app.ImageName == "" {
			return fmt.Errorf("%s app doesn't have imagename property", app.AppName)
		}
		app.ImageName = fmt.Sprintf("%s:%s", app.ImageName, c.imageTag())

		log.Printf("Adding app %v", app)
		c.AppResources.Add(kube.NewAppManager(c.kubeClient, kube.DaprTestNamespace, app))
	}

	// installApps installs the apps in AppResource queue sequentially
	if err := c.AppResources.setup(); err != nil {
		return err
	}

	return nil
}

func (c *KubeTestPlatform) imageRegistry() string {
	reg := os.Getenv("DAPR_TEST_REGISTRY")
	if reg == "" {
		return defaultImageRegistry
	}
	return reg
}

func (c *KubeTestPlatform) imageTag() string {
	tag := os.Getenv("DAPR_TEST_TAG")
	if tag == "" {
		return defaultImageTag
	}
	return tag
}

// AcquireAppExternalURL returns the external url for 'name'.
func (c *KubeTestPlatform) AcquireAppExternalURL(name string) string {
	app := c.AppResources.FindActiveResource(name)
	return app.(*kube.AppManager).AcquireExternalURL()
}

// GetAppHostDetails returns the name and IP address of the host(pod) running 'name'
func (c *KubeTestPlatform) GetAppHostDetails(name string) (string, string, error) {
	app := c.AppResources.FindActiveResource(name)
	hostname, hostIP, err := app.(*kube.AppManager).GetHostDetails()
	if err != nil {
		return "", "", err
	}

	return hostname, hostIP, nil
}

// Scale changes the number of replicas of the app
func (c *KubeTestPlatform) Scale(name string, replicas int32) error {
	app := c.AppResources.FindActiveResource(name)
	appManager := app.(*kube.AppManager)

	if err := appManager.ScaleDeploymentReplica(replicas); err != nil {
		return err
	}

	_, err := appManager.WaitUntilDeploymentState(appManager.IsDeploymentDone)

	return err
}

// Restart restarts all instances for the app.
func (c *KubeTestPlatform) Restart(name string) error {
	// To minic the restart behavior, scale to 0 and then scale to the original replicas.
	app := c.AppResources.FindActiveResource(name)
	originalReplicas := app.(*kube.AppManager).App().Replicas

	if err := c.Scale(name, 0); err != nil {
		return err
	}

	return c.Scale(name, originalReplicas)
}

// PortForwardToApp opens a new connection to the app on a the target port and returns the local port or error.
func (c *KubeTestPlatform) PortForwardToApp(appName string, targetPorts ...int) ([]int, error) {
	app := c.AppResources.FindActiveResource(appName)
	appManager := app.(*kube.AppManager)

	_, err := appManager.WaitUntilDeploymentState(appManager.IsDeploymentDone)
	if err != nil {
		return nil, err
	}

	if targetPorts == nil {
		return nil, fmt.Errorf("cannot open connection with no target ports")
	}
	return appManager.DoPortForwarding("", targetPorts...)
}
