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

package ca

import (
	"context"
	"fmt"
	"os"

	"github.com/dapr/dapr/pkg/sentry/config"
)

// selfSigned is a store that uses the file system as the secret store.
type selfhosted struct {
	config config.Config
}

func (s *selfhosted) store(_ context.Context, bundle Bundle) error {
	for _, f := range []struct {
		name string
		data []byte
	}{
		{s.config.RootCertPath, bundle.TrustAnchors},
		{s.config.IssuerCertPath, bundle.IssChainPEM},
		{s.config.IssuerKeyPath, bundle.IssKeyPEM},
	} {
		if err := os.WriteFile(f.name, f.data, 0o600); err != nil {
			return err
		}
	}

	return nil
}

func (s *selfhosted) get(_ context.Context) (Bundle, bool, error) {
	trustAnchors, err := os.ReadFile(s.config.RootCertPath)
	if os.IsNotExist(err) {
		return Bundle{}, false, nil
	}
	if err != nil {
		return Bundle{}, false, err
	}

	issChainPEM, err := os.ReadFile(s.config.IssuerCertPath)
	if os.IsNotExist(err) {
		return Bundle{}, false, nil
	}
	if err != nil {
		return Bundle{}, false, err
	}

	issKeyPEM, err := os.ReadFile(s.config.IssuerKeyPath)
	if os.IsNotExist(err) {
		return Bundle{}, false, nil
	}
	if err != nil {
		return Bundle{}, false, err
	}

	bundle, err := verifyBundle(trustAnchors, issChainPEM, issKeyPEM)
	if err != nil {
		return Bundle{}, false, fmt.Errorf("failed to verify CA bundle: %w", err)
	}

	return bundle, true, nil
}
