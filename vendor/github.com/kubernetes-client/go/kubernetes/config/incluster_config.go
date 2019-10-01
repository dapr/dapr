/*
Copyright 2018 The Kubernetes Authors.

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

package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"

	"github.com/kubernetes-client/go/kubernetes/client"
)

const (
	serviceHostEnvName   = "KUBERNETES_SERVICE_HOST"
	servicePortEnvName   = "KUBERNETES_SERVICE_PORT"
	serviceTokenFilename = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	serviceCertFilename  = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

// InClusterConfig returns a config object which uses the service account
// kubernetes gives to pods. It's intended for clients that expect to be
// running inside a pod running on kubernetes. It will return an error if
// called from a process not running in a kubernetes environment.
func InClusterConfig() (*client.Configuration, error) {
	host, port := os.Getenv(serviceHostEnvName), os.Getenv(servicePortEnvName)
	if len(host) == 0 || len(port) == 0 {
		return nil, fmt.Errorf("unable to load in-cluster configuration, %v and %v must be defined", serviceHostEnvName, servicePortEnvName)
	}

	token, err := ioutil.ReadFile(serviceTokenFilename)
	if err != nil {
		return nil, err
	}
	caCert, err := ioutil.ReadFile(serviceCertFilename)
	if err != nil {
		return nil, err
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)
	c := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: caCertPool,
			},
		},
	}

	return &client.Configuration{
		BasePath:      "https://" + net.JoinHostPort(host, port),
		Host:          net.JoinHostPort(host, port),
		Scheme:        "https",
		DefaultHeader: map[string]string{"Authentication": "Bearer " + string(token)},
		UserAgent:     defaultUserAgent,
		HTTPClient:    c,
	}, nil
}
