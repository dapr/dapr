/*
Copyright 2022 The Dapr Authors
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

package loadtest

import (
	"context"
	"time"

	"github.com/dapr/dapr/pkg/client/clientset/versioned/scheme"

	v1 "github.com/grafana/k6-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
)

type K6Interface interface {
	Create(ctx context.Context, k6 *v1.K6) (*v1.K6, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
	Get(ctx context.Context, name string) (*v1.K6, error)
	List(ctx context.Context, opts metav1.ListOptions) (result *v1.K6List, err error)
}

// k6 implements K6Interface
type k6 struct {
	client rest.Interface
	ns     string
}

// newK6Client returns a k6
func newK6Client(crdConfig *rest.Config, namespace string) (*k6, error) {
	v1.AddToScheme(scheme.Scheme)
	crdConfig.ContentConfig.GroupVersion = &v1.GroupVersion
	crdConfig.APIPath = "/apis"
	crdConfig.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme)
	crdConfig.UserAgent = rest.DefaultKubernetesUserAgent()
	restClient, err := rest.RESTClientFor(crdConfig)
	if err != nil {
		return nil, err
	}
	return &k6{
		client: restClient,
		ns:     namespace,
	}, nil
}

// Delete takes name of the k6 and deletes it. Returns an error if one occurs.
func (c *k6) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("k6s").
		Name(name).
		Body(&opts).
		Do(ctx).
		Error()
}

// Get takes name of the k6, and returns the corresponding k6 object, and an error if there is any.
func (c *k6) Get(ctx context.Context, name string) (result *v1.K6, err error) {
	result = &v1.K6{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("k6s").
		Name(name).
		Do(ctx).
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of K6s that match those selectors.
func (c *k6) List(ctx context.Context, opts metav1.ListOptions) (result *v1.K6List, err error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	result = &v1.K6List{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("k6s").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Do(ctx).
		Into(result)
	return
}

// Create takes the representation of a k6 and creates it.  Returns the server's representation of the k6, and an error, if there is any.
func (c *k6) Create(ctx context.Context, k6test *v1.K6) (result *v1.K6, err error) {
	result = &v1.K6{}
	err = c.client.Post().
		Namespace(c.ns).
		Resource("k6s").
		Body(k6test).
		Do(ctx).
		Into(result)
	return
}
