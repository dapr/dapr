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

package kubernetes

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/utils"
)

const (
	// TestAppLabelKey is the label key for Kubernetes label selector.
	TestAppLabelKey = "testapp"
	// DaprSideCarName is the Pod name of Dapr side car.
	DaprSideCarName = "daprd"

	// DefaultContainerPort is the default container port exposed from test app.
	DefaultContainerPort = 3000
	// DefaultExternalPort is the default external port exposed by load balancer ingress.
	DefaultExternalPort = 3000

	// DaprComponentsKind is component kind.
	DaprComponentsKind = "components.dapr.io"

	// DaprTestNamespaceEnvVar is the environment variable for setting the Kubernetes namespace for e2e tests.
	DaprTestNamespaceEnvVar = "DAPR_TEST_NAMESPACE"

	// TargetOsEnvVar Environment variable for setting Kubernetes node affinity OS.
	TargetOsEnvVar = "TARGET_OS"

	// TargetArchEnvVar Environment variable for setting Kubernetes node affinity ARCH.
	TargetArchEnvVar = "TARGET_ARCH"

	// Environmental variable to disable API logging
	DisableAPILoggingEnvVar = "NO_API_LOGGING"
)

var (
	// DaprTestNamespace is the default Kubernetes namespace for e2e tests.
	DaprTestNamespace = "dapr-tests"

	// TargetOs is default os affinity for Kubernetes nodes.
	TargetOs = "linux"

	// TargetArch is the default architecture affinity for Kubernetes nodes.
	TargetArch = "amd64"

	// Controls whether API logging is enabled
	EnableAPILogging = true
)

// buildDaprAnnotations creates the Kubernetes Annotations object for dapr test app.
func buildDaprAnnotations(appDesc AppDescription) map[string]string {
	annotationObject := map[string]string{}

	if appDesc.DaprEnabled {
		annotationObject = map[string]string{
			"dapr.io/enabled":                           "true",
			"dapr.io/app-id":                            appDesc.AppName,
			"dapr.io/sidecar-cpu-limit":                 appDesc.DaprCPULimit,
			"dapr.io/sidecar-cpu-request":               appDesc.DaprCPURequest,
			"dapr.io/sidecar-memory-limit":              appDesc.DaprMemoryLimit,
			"dapr.io/sidecar-memory-request":            appDesc.DaprMemoryRequest,
			"dapr.io/sidecar-readiness-probe-threshold": "15",
			"dapr.io/sidecar-liveness-probe-threshold":  "15",
			"dapr.io/enable-metrics":                    strconv.FormatBool(appDesc.MetricsEnabled),
			"dapr.io/enable-api-logging":                strconv.FormatBool(EnableAPILogging),
			"dapr.io/disable-builtin-k8s-secret-store":  strconv.FormatBool(appDesc.SecretStoreDisable),
		}
		if !appDesc.IsJob {
			annotationObject["dapr.io/app-port"] = fmt.Sprintf("%d", appDesc.AppPort)
		}
	}
	if appDesc.AppProtocol != "" {
		annotationObject["dapr.io/app-protocol"] = appDesc.AppProtocol
	}
	if appDesc.MetricsPort != "" {
		annotationObject["dapr.io/metrics-port"] = appDesc.MetricsPort
	}
	if appDesc.Config != "" {
		annotationObject["dapr.io/config"] = appDesc.Config
	}
	if appDesc.DaprVolumeMounts != "" {
		annotationObject["dapr.io/volume-mounts"] = appDesc.DaprVolumeMounts
	}

	if len(appDesc.PlacementAddresses) != 0 {
		annotationObject["dapr.io/placement-host-address"] = strings.Join(appDesc.PlacementAddresses, ",")
	}
	return annotationObject
}

// buildPodTemplate creates the Kubernetes Pod Template object for dapr test app.
func buildPodTemplate(appDesc AppDescription) apiv1.PodTemplateSpec {
	appEnv := []apiv1.EnvVar{}
	if appDesc.AppEnv != nil {
		for key, value := range appDesc.AppEnv {
			appEnv = append(appEnv, apiv1.EnvVar{
				Name:  key,
				Value: value,
			})
		}
	}

	labels := appDesc.Labels
	if len(labels) == 0 {
		labels = make(map[string]string, 1)
	}
	labels[TestAppLabelKey] = appDesc.AppName

	var podAffinity *apiv1.PodAffinity
	if len(appDesc.PodAffinityLabels) > 0 {
		podAffinity = &apiv1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []apiv1.PodAffinityTerm{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: appDesc.PodAffinityLabels,
					},
					TopologyKey: "topology.kubernetes.io/zone",
				},
			},
		}
	}

	return apiv1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      labels,
			Annotations: buildDaprAnnotations(appDesc),
		},
		Spec: apiv1.PodSpec{
			InitContainers: appDesc.InitContainers,
			Containers: []apiv1.Container{
				{
					Name:            appDesc.AppName,
					Image:           fmt.Sprintf("%s/%s", appDesc.RegistryName, appDesc.ImageName),
					ImagePullPolicy: apiv1.PullAlways,
					Ports: []apiv1.ContainerPort{
						{
							Name:          "http",
							Protocol:      apiv1.ProtocolTCP,
							ContainerPort: DefaultContainerPort,
						},
					},
					Env: appEnv,
				},
			},
			Affinity: &apiv1.Affinity{
				NodeAffinity: &apiv1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &apiv1.NodeSelector{
						NodeSelectorTerms: []apiv1.NodeSelectorTerm{
							{
								MatchExpressions: []apiv1.NodeSelectorRequirement{
									{
										Key:      "kubernetes.io/os",
										Operator: "In",
										Values:   []string{TargetOs},
									},
									{
										Key:      "kubernetes.io/arch",
										Operator: "In",
										Values:   []string{TargetArch},
									},
								},
							},
						},
					},
				},
				PodAffinity: podAffinity,
			},
			ImagePullSecrets: []apiv1.LocalObjectReference{
				{
					Name: appDesc.ImageSecret,
				},
			},
			Volumes: appDesc.Volumes,
		},
	}
}

// buildDeploymentObject creates the Kubernetes Deployment object for dapr test app.
func buildDeploymentObject(namespace string, appDesc AppDescription) *appsv1.Deployment {
	if appDesc.AppPort == 0 { // If AppPort is negative, assume this has been set explicitly
		appDesc.AppPort = DefaultContainerPort
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appDesc.AppName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(appDesc.Replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					TestAppLabelKey: appDesc.AppName,
				},
			},
			Template: buildPodTemplate(appDesc),
		},
	}
}

// buildJobObject creates the Kubernetes Job object for dapr test app.
func buildJobObject(namespace string, appDesc AppDescription) *batchv1.Job {
	if appDesc.AppPort == 0 { // If AppPort is negative, assume this has been set explicitly
		appDesc.AppPort = DefaultContainerPort
	}
	job := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appDesc.AppName,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			Template: buildPodTemplate(appDesc),
		},
	}
	job.Spec.Template.Spec.RestartPolicy = apiv1.RestartPolicyOnFailure
	return &job
}

// buildServiceObject creates the Kubernetes Service Object for dapr test app.
func buildServiceObject(namespace string, appDesc AppDescription) *apiv1.Service {
	serviceType := apiv1.ServiceTypeClusterIP

	if appDesc.IngressEnabled {
		serviceType = apiv1.ServiceTypeLoadBalancer
	}

	targetPort := DefaultContainerPort
	if appDesc.AppPort > 0 {
		targetPort = appDesc.AppPort
	}

	return &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appDesc.AppName,
			Namespace: namespace,
			Labels: map[string]string{
				TestAppLabelKey: appDesc.AppName,
			},
		},
		Spec: apiv1.ServiceSpec{
			Selector: map[string]string{
				TestAppLabelKey: appDesc.AppName,
			},
			Ports: []apiv1.ServicePort{
				{
					Protocol:   apiv1.ProtocolTCP,
					Port:       DefaultExternalPort,
					TargetPort: intstr.IntOrString{IntVal: int32(targetPort)},
				},
			},
			Type: serviceType,
		},
	}
}

// buildDaprComponentObject creates dapr component object.
func buildDaprComponentObject(componentName string, typeName string, metaData []v1alpha1.MetadataItem) *v1alpha1.Component {
	return &v1alpha1.Component{
		TypeMeta: metav1.TypeMeta{
			Kind: DaprComponentsKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: componentName,
		},
		Spec: v1alpha1.ComponentSpec{
			Type:     typeName,
			Metadata: metaData,
		},
	}
}

// buildNamespaceObject creates the Kubernetes Namespace object.
func buildNamespaceObject(namespace string) *apiv1.Namespace {
	return &apiv1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
}

func int32Ptr(i int32) *int32 {
	return &i
}

func init() {
	if ns, ok := os.LookupEnv(DaprTestNamespaceEnvVar); ok {
		DaprTestNamespace = ns
	}
	if os, ok := os.LookupEnv(TargetOsEnvVar); ok {
		TargetOs = os
	}
	if arch, ok := os.LookupEnv(TargetArchEnvVar); ok {
		TargetArch = arch
	}
	{
		v, _ := os.LookupEnv(DisableAPILoggingEnvVar)
		EnableAPILogging = !utils.IsTruthy(v)
	}
}
