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
	"io/ioutil"
	"time"
)

var (
	// time.Duration that prevents the client dropping valid credential
	// due to time skew
	expirySkewPreventionDelay = 5 * time.Minute
)

// Read authentication from kube-config user section if exists.
//
// This function goes through various authentication methods in user
// section of kube-config and stops if it finds a valid authentication
// method. The order of authentication methods is:
//
//     1. GCP auth-provider
//     2. token_data
//     3. token field (point to a token file)
//     4. username/password
func (l *KubeConfigLoader) loadAuthentication() {
	// The function walks though authentication methods. It doesn't fail on
	// single method loading failure. It is each loading function's responsiblity
	// to log meaningful failure message. Kubeconfig is allowed to have no user
	// in current context, therefore it is allowed that no authentication is loaded.

	// TODO: This structure is ugly, there should be an 'authenticator' interface
	if l.loadGCPToken() || l.loadUserToken() || l.loadUserPassToken() || l.loadAzureToken() {
		return
	}
}

func (l *KubeConfigLoader) loadUserToken() bool {
	if l.user.Token == "" && l.user.TokenFile == "" {
		return false
	}
	// Token takes precedence than TokenFile
	if l.user.Token != "" {
		l.restConfig.token = "Bearer " + l.user.Token
		return true
	}

	// Read TokenFile
	token, err := ioutil.ReadFile(l.user.TokenFile)
	if err != nil {
		// A user may not provide any TokenFile, so we don't log error here
		return false
	}
	l.restConfig.token = "Bearer " + string(token)
	return true
}

func (l *KubeConfigLoader) loadUserPassToken() bool {
	if l.user.Username != "" && l.user.Password != "" {
		l.restConfig.token = basicAuthToken(l.user.Username, l.user.Password)
		return true
	}
	return false
}
