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
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/kubernetes-client/go/kubernetes/config/api"
)

// DataOrFile reads content of data, or file's content if data doesn't exist
// and represent it as []byte data.
func DataOrFile(data []byte, file string) ([]byte, error) {
	if data != nil {
		return data, nil
	}
	result, err := ioutil.ReadFile(file)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get data or file (%s): %v", file, err)
	}
	return result, nil
}

// isExpired returns true if the token expired in expirySkewPreventionDelay time (default is 5 minutes)
func isExpired(timestamp string) (bool, error) {
	ts, err := time.Parse(gcpRFC3339Format, timestamp)
	if err != nil {
		return false, err
	}
	return ts.Before(time.Now().UTC().Add(expirySkewPreventionDelay)), nil
}

func basicAuthToken(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

func getContextWithName(contexts []api.NamedContext, name string) (*api.Context, error) {
	var context *api.Context
	for _, c := range contexts {
		if c.Name == name {
			if context != nil {
				return nil, fmt.Errorf("error parsing kube config: duplicate contexts with name %v", name)
			}
			context = c.Context.DeepCopy()
		}
	}
	if context == nil {
		return nil, fmt.Errorf("error parsing kube config: couldn't find context with name %v", name)
	}
	return context, nil
}

func getClusterWithName(clusters []api.NamedCluster, name string) (*api.Cluster, error) {
	var cluster *api.Cluster
	for _, c := range clusters {
		if c.Name == name {
			if cluster != nil {
				return nil, fmt.Errorf("error parsing kube config: duplicate clusters with name %v", name)
			}
			cluster = c.Cluster.DeepCopy()
		}
	}
	if cluster == nil {
		return nil, fmt.Errorf("error parsing kube config: couldn't find cluster with name %v", name)
	}
	return cluster, nil
}

func getUserWithName(users []api.NamedAuthInfo, name string) (*api.AuthInfo, error) {
	var user *api.AuthInfo
	for _, u := range users {
		if u.Name == name {
			if user != nil {
				return nil, fmt.Errorf("error parsing kube config: duplicate users with name %v", name)
			}
			user = u.AuthInfo.DeepCopy()
		}
	}
	// A context may have no user, or using non-existing user name. We simply return nil *AuthInfo in this case.
	return user, nil
}

func setUserWithName(users []api.NamedAuthInfo, name string, user *api.AuthInfo) error {
	var userFound bool
	var userTarget *api.AuthInfo

	for i, u := range users {
		if u.Name == name {
			if userFound {
				return fmt.Errorf("error setting kube config: duplicate users with name %v", name)
			}
			userTarget = &users[i].AuthInfo
			userFound = true
		}
	}
	if !userFound {
		return fmt.Errorf("error setting kube config: cannot find user with name: %v", name)
	}
	user.DeepCopyInto(userTarget)
	return nil
}
