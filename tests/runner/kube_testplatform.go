/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package runner

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	configurationv1alpha1 "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
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
	Secrets            *TestResources
	KubeClient         *kube.KubeClient
}

// NewKubeTestPlatform creates KubeTestPlatform instance.
func NewKubeTestPlatform() *KubeTestPlatform {
	return &KubeTestPlatform{
		AppResources:       new(TestResources),
		ComponentResources: new(TestResources),
		Secrets:            new(TestResources),
	}
}

func (c *KubeTestPlatform) Setup() (err error) {
	// TODO: KubeClient will be properly configured by go test arguments
	c.KubeClient, err = kube.NewKubeClient("", "")

	return
}

func (c *KubeTestPlatform) TearDown() error {
	if err := c.AppResources.tearDown(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to tear down AppResources. got: %q", err)
	}

	if err := c.ComponentResources.tearDown(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to tear down ComponentResources. got: %q", err)
	}

	if err := c.Secrets.tearDown(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to tear down Secrets. got: %q", err)
	}

	// TODO: clean up kube cluster

	return nil
}

// addComponents adds component to disposable Resource queues.
func (c *KubeTestPlatform) AddComponents(comps []kube.ComponentDescription) error {
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

// AddSecrets adds secrets to disposable Resource queues.
func (c *KubeTestPlatform) AddSecrets(secrets []kube.SecretDescription) error {
	if c.KubeClient == nil {
		return fmt.Errorf("kubernetes cluster needs to be setup")
	}

	for _, secret := range secrets {
		c.Secrets.Add(kube.NewSecret(c.KubeClient, secret.Namespace, secret.Name, secret.Data))
	}

	// setup secret resources
	if err := c.Secrets.setup(); err != nil {
		return err
	}

	return nil
}

// addApps adds test apps to disposable App Resource queues.
func (c *KubeTestPlatform) AddApps(apps []kube.AppDescription) error {
	if c.KubeClient == nil {
		return fmt.Errorf("kubernetes cluster needs to be setup before calling BuildAppResources")
	}

	dt := c.disableTelemetry()

	namespaces := make(map[string]struct{}, 0)
	for _, app := range apps {
		if app.RegistryName == "" {
			app.RegistryName = getTestImageRegistry()
		}

		if app.ImageSecret == "" {
			app.ImageSecret = getTestImageSecret()
		}

		if app.ImageName == "" {
			return fmt.Errorf("%s app doesn't have imagename property", app.AppName)
		}
		app.ImageName = fmt.Sprintf("%s:%s", app.ImageName, getTestImageTag())

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
		namespace := getNamespaceOrDefault(app.Namespace)
		namespaces[namespace] = struct{}{}
		c.AppResources.Add(kube.NewAppManager(c.KubeClient, namespace, app))
	}

	// Create all namespaces (if they don't already exist)
	log.Print("Ensuring namespaces exist ...")
	for namespace := range namespaces {
		_, err := c.GetOrCreateNamespace(context.Background(), namespace)
		if err != nil {
			return fmt.Errorf("failed to create namespace %q: %w", namespace, err)
		}
	}

	// installApps installs the apps in AppResource queue sequentially
	log.Print("Installing apps ...")
	if err := c.AppResources.setup(); err != nil {
		return err
	}
	log.Print("Apps are installed.")

	return nil
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

// GetOrCreateNamespace gets or creates namespace unless namespace exists.
func (c *KubeTestPlatform) GetOrCreateNamespace(parentCtx context.Context, namespace string) (*corev1.Namespace, error) {
	log.Printf("Checking namespace %q ...", namespace)
	namespaceClient := c.KubeClient.Namespaces()
	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
	ns, err := namespaceClient.Get(ctx, namespace, metav1.GetOptions{})
	cancel()

	if err != nil && errors.IsNotFound(err) {
		log.Printf("Creating namespace %q ...", namespace)
		obj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		ctx, cancel = context.WithTimeout(parentCtx, 30*time.Second)
		ns, err = namespaceClient.Create(ctx, obj, metav1.CreateOptions{})
		cancel()
		return ns, err
	}

	return ns, err
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

	if _, err := appManager.WaitUntilDeploymentState(appManager.IsDeploymentDone); err != nil {
		return err
	}

	appManager.StreamContainerLogs()

	return nil
}

// Restart restarts all instances for the app.
func (c *KubeTestPlatform) Restart(name string) error {
	// To minic the restart behavior, scale to 0 and then scale to the original replicas.
	app := c.AppResources.FindActiveResource(name)
	m := app.(*kube.AppManager)
	originalReplicas := m.App().Replicas

	if err := c.Scale(name, 0); err != nil {
		return err
	}

	if err := c.Scale(name, originalReplicas); err != nil {
		return err
	}

	m.StreamContainerLogs()

	return nil
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

// GetConfiguration returns configuration by name.
func (c *KubeTestPlatform) GetConfiguration(name string) (*configurationv1alpha1.Configuration, error) {
	client := c.KubeClient.DaprClientSet.ConfigurationV1alpha1().Configurations(kube.DaprTestNamespace)
	return client.Get(name, metav1.GetOptions{})
}

func (c *KubeTestPlatform) GetService(name string) (*corev1.Service, error) {
	client := c.KubeClient.Services(kube.DaprTestNamespace)
	return client.Get(context.Background(), name, metav1.GetOptions{})
}

func (c *KubeTestPlatform) LoadTest(loadtester LoadTester) error {
	return loadtester.Run(c)
}
