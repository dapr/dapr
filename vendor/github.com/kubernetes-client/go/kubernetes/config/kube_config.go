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
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/kubernetes-client/go/kubernetes/client"
	"github.com/kubernetes-client/go/kubernetes/config/api"
)

const (
	defaultUserAgent          = "Swagger-Codegen/0.1.0a1/go"
	kubeConfigEnvName         = "KUBECONFIG"
	kubeConfigDefaultFilename = ".kube/config"
)

// KubeConfigLoader implements the util functions to load authentication and cluster
// info and hosts intermediate info values.
type KubeConfigLoader struct {
	rawConfig  api.Config
	restConfig RestConfig

	// Skip config persistence, default to false
	skipConfigPersist bool
	configFilename    string

	// Current cluster, user and context
	cluster        api.Cluster
	user           api.AuthInfo
	currentContext api.Context

	// Set this interface to pass in custom Google credential loader instead of
	// using the default loader
	gcLoader GoogleCredentialLoader
}

// RestConfig contains the information that a rest client needs to talk with a server
type RestConfig struct {
	basePath string
	host     string
	scheme   string

	// authentication token
	token string

	// TLS info
	caCert     []byte
	clientCert []byte
	clientKey  []byte

	// skip TLS verification, default to false
	skipTLSVerify bool
}

// LoadKubeConfig loads authentication and cluster information from kube-config file
// and stores them in returned client.Configuration.
func LoadKubeConfig() (*client.Configuration, error) {
	kubeConfigFilename := os.Getenv(kubeConfigEnvName)
	// Fallback to default kubeconfig file location if no env variable set
	if kubeConfigFilename == "" {
		kubeConfigFilename = path.Join(os.Getenv("HOME"), kubeConfigDefaultFilename)
	}

	loader, err := NewKubeConfigLoaderFromYAMLFile(kubeConfigFilename, false)
	if err != nil {
		return nil, err
	}

	return loader.LoadAndSet()
}

// NewKubeConfigLoaderFromYAMLFile creates a new KubeConfigLoader with a parsed
// config yaml file.
func NewKubeConfigLoaderFromYAMLFile(filename string, skipConfigPersist bool) (*KubeConfigLoader, error) {
	kubeConfig, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	// Init an empty Config as unmarshal layout template
	c := api.Config{}
	if err := yaml.Unmarshal(kubeConfig, &c); err != nil {
		return nil, err
	}

	l := KubeConfigLoader{
		rawConfig:         c,
		skipConfigPersist: skipConfigPersist,
		configFilename:    filename,
	}

	// Init loader with current cluster, user and context
	if err := l.LoadActiveContext(); err != nil {
		return nil, err
	}
	return &l, nil
}

// LoadAndSet loads authentication and cluster information from kube-config file and
// stores them in returned Configuration.
func (l *KubeConfigLoader) LoadAndSet() (*client.Configuration, error) {
	l.loadAuthentication()

	if err := l.loadClusterInfo(); err != nil {
		return nil, err
	}
	return l.setConfig()
}

// loadClusterInfo uses the current cluster, user and context info stored in loader and
// gets necessary TLS information
func (l *KubeConfigLoader) loadClusterInfo() error {
	// The swagger-codegen go client doesn't work well with base path having trailing slash.
	// This is a short term fix.
	l.restConfig.basePath = strings.TrimRight(l.cluster.Server, "/")

	u, err := url.Parse(l.cluster.Server)
	if err != nil {
		return err
	}
	l.restConfig.host = u.Host
	l.restConfig.scheme = u.Scheme

	if l.cluster.InsecureSkipTLSVerify {
		l.restConfig.skipTLSVerify = true
	}

	if l.restConfig.scheme == "https" {
		if !l.restConfig.skipTLSVerify {
			l.restConfig.caCert, err = DataOrFile(l.cluster.CertificateAuthorityData, l.cluster.CertificateAuthority)
			if err != nil {
				return fmt.Errorf("Error loading certificate-authority: %s", err.Error())
			}
		}
		l.restConfig.clientCert, err = DataOrFile(l.user.ClientCertificateData, l.user.ClientCertificate)
		if err != nil {
			return fmt.Errorf("Error loading client certificate: %s", err.Error())
		}
		l.restConfig.clientKey, err = DataOrFile(l.user.ClientKeyData, l.user.ClientKey)
		if err != nil {
			return fmt.Errorf("Error loading client key: %s", err.Error())
		}
	}

	return nil
}

// setConfig converts authentication and TLS info into client Configuration
func (l *KubeConfigLoader) setConfig() (*client.Configuration, error) {
	// Set TLS info
	transport := http.Transport{}
	if l.restConfig.scheme == "https" {
		certs := []tls.Certificate{}
		if len(l.restConfig.clientCert) > 0 || len(l.restConfig.clientKey) > 0 {
			cert, err := tls.X509KeyPair(l.restConfig.clientCert, l.restConfig.clientKey)
			if err != nil {
				return nil, err
			}
			certs = append(certs, cert)
		}
		caCertPool := x509.NewCertPool()
		if len(l.restConfig.caCert) > 0 {
			caCertPool.AppendCertsFromPEM(l.restConfig.caCert)
		}
		transport.TLSClientConfig = &tls.Config{
			RootCAs:            caCertPool,
			Certificates:       certs,
			InsecureSkipVerify: l.restConfig.skipTLSVerify,
		}
	}

	c := &http.Client{
		Transport: &transport,
	}

	header := make(map[string]string)
	// Add authentication info to default header
	if l.restConfig.token != "" {
		header["Authorization"] = l.restConfig.token
		// Handle Golang dropping headers on redirect
		c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			req.Header.Add("Authorization", l.restConfig.token)
			return nil
		}
	}

	return &client.Configuration{
		BasePath:      l.restConfig.basePath,
		Host:          l.restConfig.host,
		Scheme:        l.restConfig.scheme,
		DefaultHeader: header,
		UserAgent:     defaultUserAgent,
		HTTPClient:    c,
	}, nil
}

// RestConfig returns the value of RestConfig in a KubeConfigLoader
func (l *KubeConfigLoader) RestConfig() RestConfig {
	return l.restConfig
}

// SetActiveContext sets the active context in rawConfig, performs necessary persistence,
// and reload active context. This function enables context switch
func (l *KubeConfigLoader) SetActiveContext(ctx string) error {
	currentContext, err := getContextWithName(l.rawConfig.Contexts, ctx)
	if err != nil {
		return err
	}
	currentContext.DeepCopyInto(&l.currentContext)
	l.rawConfig.CurrentContext = ctx

	// Persist kube config file
	if !l.skipConfigPersist {
		if err := l.persistConfig(); err != nil {
			return err
		}
	}

	return l.LoadActiveContext()
}

// LoadActiveContext parses the loader's rawConfig using current context and set loader's
// current cluster and user.
func (l *KubeConfigLoader) LoadActiveContext() error {
	currentContext, err := getContextWithName(l.rawConfig.Contexts, l.rawConfig.CurrentContext)
	if err != nil {
		return err
	}
	currentContext.DeepCopyInto(&l.currentContext)

	cluster, err := getClusterWithName(l.rawConfig.Clusters, l.currentContext.Cluster)
	if err != nil {
		return err
	}
	cluster.DeepCopyInto(&l.cluster)

	user, err := getUserWithName(l.rawConfig.AuthInfos, l.currentContext.AuthInfo)
	if err != nil {
		return err
	}

	// kube config may have no (current) user
	if user != nil {
		user.DeepCopyInto(&l.user)
	}
	return nil
}

// persisConfig saves the stored rawConfig to the config file. This function is not exposed and
// should be called only when skipConfigPersist is false.
// TODO(roycaihw): enable custom persistConfig function
func (l *KubeConfigLoader) persistConfig() error {
	if l.skipConfigPersist {
		return nil
	}
	data, err := yaml.Marshal(l.rawConfig)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(l.configFilename, data, 0644)
}
