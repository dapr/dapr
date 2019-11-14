// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runner

import (
	"fmt"
	"os"

	log "github.com/Sirupsen/logrus"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
)

const (
	defaultImageRegistry = "docker.io/dapriotest"
	defaultImageTag      = "latest"
)

// KubeTestPlatform includes K8s client for testing cluster and kubernetes testing apps
type KubeTestPlatform struct {
	AppResources *TestResources
	kubeClient   *kube.KubeClient
}

// NewKubeTestPlatform creates KubeTestPlatform instance
func NewKubeTestPlatform() *KubeTestPlatform {
	return &KubeTestPlatform{
		AppResources: new(TestResources),
	}
}

func (c *KubeTestPlatform) setup() (err error) {
	// TODO: KubeClient will be properly configured by go test arguments
	c.kubeClient, err = kube.NewKubeClient("", "")

	return
}

func (c *KubeTestPlatform) tearDown() error {
	if err := c.AppResources.tearDown(); err != nil {
		log.Errorf("Failed to tearDown AppResources. got: %q", err)
	}

	// TODO: clean up kube cluster

	return nil
}

// addApps adds test apps to disposable App Resource queues
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

		c.AppResources.Add(kube.NewAppManager(c.kubeClient, kube.DaprTestKubeNameSpace, app))
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

// installApps installs the apps in AppResource queue sequentially
func (c *KubeTestPlatform) installApps() error {
	if err := c.AppResources.setup(); err != nil {
		return err
	}
	return nil
}

// AcquireAppExternalURL returns the external url for 'name'
func (c *KubeTestPlatform) AcquireAppExternalURL(name string) string {
	app := c.AppResources.FindActiveResource(name)
	return app.(*kube.AppManager).AcquireExternalURL()
}
