// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	"fmt"
	"os"

	v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// TestAppLabelKey is the label key for Kubernetes label selector
	TestAppLabelKey = "testapp"
	// DaprSideCarName is the Pod name of Dapr side car
	DaprSideCarName = "daprd"

	// DefaultContainerPort is the default container port exposed from test app
	DefaultContainerPort = 3000
	// DefaultExternalPort is the default external port exposed by load balancer ingress
	DefaultExternalPort = 3000

	// DaprComponentsKind is component kind
	DaprComponentsKind = "components.dapr.io"

	// DaprTestNamespaceEnvVar is the environment variable for setting the Kubernetes namespace for e2e tests
	DaprTestNamespaceEnvVar = "DAPR_TEST_NAMESPACE"
)

// DaprTestNamespace is the default Kubernetes namespace for e2e tests
var DaprTestNamespace = "dapr-tests"

// buildDeploymentObject creates the Kubernetes Deployment object for dapr test app
func buildDeploymentObject(namespace string, appDesc AppDescription) *appsv1.Deployment {
	annotationObject := map[string]string{}

	if appDesc.AppPort <= 0 {
		appDesc.AppPort = DefaultContainerPort
	}

	if appDesc.DaprEnabled {
		annotationObject = map[string]string{
			"dapr.io/enabled": "true",
			"dapr.io/id":      appDesc.AppName,
			"dapr.io/port":    fmt.Sprintf("%d", appDesc.AppPort),
		}
	}
	if appDesc.AppProtocol != "" {
		annotationObject["dapr.io/protocol"] = appDesc.AppProtocol
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
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						TestAppLabelKey: appDesc.AppName,
					},
					Annotations: annotationObject,
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Name:            appDesc.AppName,
							Image:           fmt.Sprintf("%s/%s", appDesc.RegistryName, appDesc.ImageName),
							ImagePullPolicy: "Always",
							Ports: []apiv1.ContainerPort{
								{
									Name:          "http",
									Protocol:      apiv1.ProtocolTCP,
									ContainerPort: DefaultContainerPort,
								},
							},
						},
					},
				},
			},
		},
	}
}

// buildServiceObject creates the Kubernetes Service Object for dapr test app
func buildServiceObject(namespace string, appDesc AppDescription) *apiv1.Service {
	serviceType := apiv1.ServiceTypeClusterIP

	if appDesc.IngressEnabled {
		serviceType = apiv1.ServiceTypeLoadBalancer
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
					TargetPort: intstr.IntOrString{IntVal: DefaultContainerPort},
				},
			},
			Type: serviceType,
		},
	}
}

// buildDaprComponentObject creates dapr component object
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

// buildNamespaceObject creates the Kubernetes Namespace object
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
}
