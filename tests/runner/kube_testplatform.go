// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runner

import (
	"fmt"
	"log"
	"os"
	"strconv"

	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
)

const (
	defaultImageRegistry        = "docker.io/dapriotest"
	defaultImageTag             = "latest"
	disableTelemetryConfig      = "disable-telemetry"
	defaultSidecarCPULimit      = "1.0"
	defaultSidecarMemoryLimit   = "256Mi"
	defaultSidecarCPURequest    = "0.1"
	defaultSidecarMemoryRequest = "100Mi"
	defaultAppCPULimit          = "1.0"
	defaultAppMemoryLimit       = "300Mi"
	defaultAppCPURequest        = "0.1"
	defaultAppMemoryRequest     = "200Mi"
)

// KubeTestPlatform includes K8s client for testing cluster and kubernetes testing apps.
type KubeTestPlatform struct {
	AppResources       *TestResources
	ComponentResources *TestResources
	KubeClient         *kube.KubeClient
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
	c.KubeClient, err = kube.NewKubeClient("", "")

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
	if c.KubeClient == nil {
		return fmt.Errorf("kubernetes cluster needs to be setup")
	}

	for _, comp := range comps {
		c.ComponentResources.Add(kube.NewDaprComponent(c.KubeClient, getNamespaceOrDefault(comp.Namespace), comp))
	}

	// setup component resources
	if err := c.ComponentResources.setup(); err != nil {
		return err
	}

	return nil
}

// addApps adds test apps to disposable App Resource queues.
func (c *KubeTestPlatform) addApps(apps []kube.AppDescription) error {
	if c.KubeClient == nil {
		return fmt.Errorf("kubernetes cluster needs to be setup before calling BuildAppResources")
	}

	dt := c.disableTelemetry()

	for _, app := range apps {
		if app.RegistryName == "" {
			app.RegistryName = c.imageRegistry()
		}

		if app.ImageSecret == "" {
			app.ImageSecret = c.imageSecret()
		}

		if app.ImageName == "" {
			return fmt.Errorf("%s app doesn't have imagename property", app.AppName)
		}
		app.ImageName = fmt.Sprintf("%s:%s", app.ImageName, c.imageTag())

		if dt {
			app.Config = disableTelemetryConfig
		}

		if app.DaprCPULimit == "" {
			app.DaprCPULimit = c.sidecarCPULimit()
		}
		if app.DaprCPURequest == "" {
			app.DaprCPURequest = c.sidecarCPURequest()
		}
		if app.DaprMemoryLimit == "" {
			app.DaprMemoryLimit = c.sidecarMemoryLimit()
		}
		if app.DaprMemoryRequest == "" {
			app.DaprMemoryRequest = c.sidecarMemoryRequest()
		}
		if app.AppCPULimit == "" {
			app.AppCPULimit = c.appCPULimit()
		}
		if app.AppCPURequest == "" {
			app.AppCPURequest = c.appCPURequest()
		}
		if app.AppMemoryLimit == "" {
			app.AppMemoryLimit = c.appMemoryLimit()
		}
		if app.AppMemoryRequest == "" {
			app.AppMemoryRequest = c.appMemoryRequest()
		}

		log.Printf("Adding app %v", app)
		c.AppResources.Add(kube.NewAppManager(c.KubeClient, getNamespaceOrDefault(app.Namespace), app))
	}

	// installApps installs the apps in AppResource queue sequentially
	log.Printf("Installing apps ...")
	if err := c.AppResources.setup(); err != nil {
		return err
	}
	log.Printf("Apps are installed.")

	return nil
}

func (c *KubeTestPlatform) imageRegistry() string {
	reg := os.Getenv("DAPR_TEST_REGISTRY")
	if reg == "" {
		return defaultImageRegistry
	}
	return reg
}

func (c *KubeTestPlatform) imageSecret() string {
	secret := os.Getenv("DAPR_TEST_REGISTRY_SECRET")
	if secret == "" {
		return ""
	}
	return secret
}

func (c *KubeTestPlatform) imageTag() string {
	tag := os.Getenv("DAPR_TEST_TAG")
	if tag == "" {
		return defaultImageTag
	}
	return tag
}

func (c *KubeTestPlatform) disableTelemetry() bool {
	disableVal := os.Getenv("DAPR_DISABLE_TELEMETRY")
	disable, err := strconv.ParseBool(disableVal)
	if err != nil {
		return false
	}
	return disable
}

func (c *KubeTestPlatform) sidecarCPULimit() string {
	cpu := os.Getenv("DAPR_SIDECAR_CPU_LIMIT")
	if cpu != "" {
		return cpu
	}
	return defaultSidecarCPULimit
}

func (c *KubeTestPlatform) sidecarCPURequest() string {
	cpu := os.Getenv("DAPR_SIDECAR_CPU_REQUEST")
	if cpu != "" {
		return cpu
	}
	return defaultSidecarCPURequest
}

func (c *KubeTestPlatform) sidecarMemoryRequest() string {
	mem := os.Getenv("DAPR_SIDECAR_MEMORY_REQUEST")
	if mem != "" {
		return mem
	}
	return defaultSidecarMemoryRequest
}

func (c *KubeTestPlatform) sidecarMemoryLimit() string {
	mem := os.Getenv("DAPR_SIDECAR_MEMORY_LIMIT")
	if mem != "" {
		return mem
	}
	return defaultSidecarMemoryLimit
}

func (c *KubeTestPlatform) appCPULimit() string {
	cpu := os.Getenv("DAPR_APP_CPU_LIMIT")
	if cpu != "" {
		return cpu
	}
	return defaultAppCPULimit
}

func (c *KubeTestPlatform) appCPURequest() string {
	cpu := os.Getenv("DAPR_APP_CPU_REQUEST")
	if cpu != "" {
		return cpu
	}
	return defaultAppCPURequest
}

func (c *KubeTestPlatform) appMemoryRequest() string {
	mem := os.Getenv("DAPR_APP_MEMORY_REQUEST")
	if mem != "" {
		return mem
	}
	return defaultAppMemoryRequest
}

func (c *KubeTestPlatform) appMemoryLimit() string {
	mem := os.Getenv("DAPR_APP_MEMORY_LIMIT")
	if mem != "" {
		return mem
	}
	return defaultAppMemoryLimit
}

// AcquireAppExternalURL returns the external url for 'name'.
func (c *KubeTestPlatform) AcquireAppExternalURL(name string) string {
	app := c.AppResources.FindActiveResource(name)
	return app.(*kube.AppManager).AcquireExternalURL()
}

// GetAppHostDetails returns the name and IP address of the host(pod) running 'name'.
func (c *KubeTestPlatform) GetAppHostDetails(name string) (string, string, error) {
	app := c.AppResources.FindActiveResource(name)
	pods, err := app.(*kube.AppManager).GetHostDetails()
	if err != nil {
		return "", "", err
	}

	if len(pods) == 0 {
		return "", "", fmt.Errorf("no pods found for app: %v", name)
	}

	return pods[0].Name, pods[0].IP, nil
}

// Scale changes the number of replicas of the app.
func (c *KubeTestPlatform) Scale(name string, replicas int32) error {
	app := c.AppResources.FindActiveResource(name)
	appManager := app.(*kube.AppManager)

	if err := appManager.ScaleDeploymentReplica(replicas); err != nil {
		return err
	}

	_, err := appManager.WaitUntilDeploymentState(appManager.IsDeploymentDone)

	return err
}

// SetAppEnv sets the container environment variable.
func (c *KubeTestPlatform) SetAppEnv(name, key, value string) error {
	app := c.AppResources.FindActiveResource(name)
	appManager := app.(*kube.AppManager)

	if err := appManager.SetAppEnv(key, value); err != nil {
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

// GetAppUsage returns the Cpu and Memory usage for the app container for a given app.
func (c *KubeTestPlatform) GetAppUsage(appName string) (*AppUsage, error) {
	app := c.AppResources.FindActiveResource(appName)
	appManager := app.(*kube.AppManager)

	cpu, mem, err := appManager.GetCPUAndMemory(false)
	if err != nil {
		return nil, err
	}
	return &AppUsage{
		CPUm:     cpu,
		MemoryMb: mem,
	}, nil
}

// GetTotalRestarts returns the total of restarts across all pods and containers for an app.
func (c *KubeTestPlatform) GetTotalRestarts(appName string) (int, error) {
	app := c.AppResources.FindActiveResource(appName)
	appManager := app.(*kube.AppManager)

	return appManager.GetTotalRestarts()
}

// GetSidecarUsage returns the Cpu and Memory usage for the dapr container for a given app.
func (c *KubeTestPlatform) GetSidecarUsage(appName string) (*AppUsage, error) {
	app := c.AppResources.FindActiveResource(appName)
	appManager := app.(*kube.AppManager)

	cpu, mem, err := appManager.GetCPUAndMemory(true)
	if err != nil {
		return nil, err
	}
	return &AppUsage{
		CPUm:     cpu,
		MemoryMb: mem,
	}, nil
}

func getNamespaceOrDefault(namespace *string) string {
	if namespace == nil {
		return kube.DaprTestNamespace
	}
	return *namespace
}
