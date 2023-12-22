/*
Copyright 2023 The Dapr Authors
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

package secret

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/dapr/components-contrib/secretstores"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compsecret "github.com/dapr/dapr/pkg/components/secretstores"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.processor.secret")

type Options struct {
	Registry       *compsecret.Registry
	ComponentStore *compstore.ComponentStore
	Meta           *meta.Meta
	OperatorClient operatorv1pb.OperatorClient
}

type secret struct {
	registry       *compsecret.Registry
	compStore      *compstore.ComponentStore
	meta           *meta.Meta
	operatorClient operatorv1pb.OperatorClient
	lock           sync.Mutex
}

func New(opts Options) *secret {
	return &secret{
		registry:       opts.Registry,
		compStore:      opts.ComponentStore,
		meta:           opts.Meta,
		operatorClient: opts.OperatorClient,
	}
}

func (s *secret) Init(ctx context.Context, comp compapi.Component) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	fName := comp.LogName()
	secretStore, err := s.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	meta, err := s.meta.ToBaseMetadata(comp)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	err = secretStore.Init(ctx, secretstores.Metadata{Base: meta})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	s.compStore.AddSecretStore(comp.ObjectMeta.Name, secretStore)
	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	return nil
}

func (s *secret) Close(comp compapi.Component) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	sec, ok := s.compStore.GetSecretStore(comp.Name)
	if !ok {
		return nil
	}

	defer s.compStore.DeleteSecretStore(comp.Name)

	closer, ok := sec.(io.Closer)
	if ok && closer != nil {
		if err := closer.Close(); err != nil {
			return err
		}
	}

	return nil
}

// Returns the component or HTTP endpoint updated with the secrets applied.
// If the resource references a secret store that hasn't been loaded yet, it returns the name of the secret store component as second returned value.
func (s *secret) ProcessResource(ctx context.Context, resource meta.Resource) (updated bool, secretStoreName string) {
	cache := map[string]secretstores.GetSecretResponse{}

	secretStoreName = s.meta.AuthSecretStoreOrDefault(resource)

	metadata := resource.NameValuePairs()
	for i, m := range metadata {
		// If there's an env var and no value, use that
		if !m.HasValue() && m.EnvRef != "" {
			if isEnvVarAllowed(m.EnvRef) {
				metadata[i].SetValue([]byte(os.Getenv(m.EnvRef)))
			} else {
				log.Warnf("%s %s references an env variable that isn't allowed: %s", resource.Kind(), resource.GetName(), m.EnvRef)
			}
			metadata[i].EnvRef = ""
			updated = true
			continue
		}

		if m.SecretKeyRef.Name == "" {
			continue
		}

		// If running in Kubernetes and have an operator client, do not fetch secrets from the Kubernetes secret store as they will be populated by the operator.
		// Instead, base64 decode the secret values into their real self.
		if s.operatorClient != nil && secretStoreName == compsecret.BuiltinKubernetesSecretStore {
			var jsonVal string
			err := json.Unmarshal(m.Value.Raw, &jsonVal)
			if err != nil {
				log.Errorf("Error decoding secret: %v", err)
				continue
			}

			dec, err := base64.StdEncoding.DecodeString(jsonVal)
			if err != nil {
				log.Errorf("Error decoding secret: %v", err)
				continue
			}

			metadata[i].SetValue(dec)
			metadata[i].SecretKeyRef = commonapi.SecretKeyRef{}
			updated = true
			continue
		}

		secretStore, ok := s.compStore.GetSecretStore(secretStoreName)
		if !ok {
			log.Warnf("%s %s references a secret store that isn't loaded: %s", resource.Kind(), resource.GetName(), secretStoreName)
			return updated, secretStoreName
		}

		resp, ok := cache[m.SecretKeyRef.Name]
		if !ok {
			r, err := secretStore.GetSecret(ctx, secretstores.GetSecretRequest{
				Name: m.SecretKeyRef.Name,
				Metadata: map[string]string{
					"namespace": resource.GetNamespace(),
				},
			})
			if err != nil {
				log.Errorf("Error getting secret: %v", err)
				continue
			}
			resp = r
		}

		// Use the SecretKeyRef.Name key if SecretKeyRef.Key is not given
		secretKeyName := m.SecretKeyRef.Key
		if secretKeyName == "" {
			secretKeyName = m.SecretKeyRef.Name
		}

		val, ok := resp.Data[secretKeyName]
		if ok && val != "" {
			metadata[i].SetValue([]byte(val))
			metadata[i].SecretKeyRef = commonapi.SecretKeyRef{}
			updated = true
		}

		cache[m.SecretKeyRef.Name] = resp
	}
	return updated, ""
}

func isEnvVarAllowed(key string) bool {
	// First, apply a denylist that blocks access to sensitive env vars
	key = strings.ToUpper(key)
	switch {
	case key == "":
		return false
	case key == "APP_API_TOKEN":
		return false
	case strings.HasPrefix(key, "DAPR_"):
		return false
	case strings.Contains(key, " "):
		return false
	}

	// If we have a `DAPR_ENV_KEYS` env var (which is added by the Dapr Injector in Kubernetes mode), use that as allowlist too
	allowlist := os.Getenv(consts.EnvKeysEnvVar)
	if allowlist == "" {
		return true
	}

	// Need to check for the full var, so there must be a space after OR it must be the end of the string, and there must be a space before OR it must be at the beginning of the string
	idx := strings.Index(allowlist, key)
	if idx >= 0 &&
		(idx+len(key) == len(allowlist) || allowlist[idx+len(key)] == ' ') &&
		(idx == 0 || allowlist[idx-1] == ' ') {
		return true
	}
	return false
}
