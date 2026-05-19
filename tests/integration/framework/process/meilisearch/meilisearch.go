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

package meilisearch

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process"
)

var _ process.Interface = (*Meilisearch)(nil)

// Meilisearch is a Meilisearch process that can be used in integration tests.
type Meilisearch struct {
	name        string
	apiKey      string
	port        int
	containerID string
}

// New creates a new Meilisearch process for integration tests.
func New(t *testing.T, fopts ...Option) *Meilisearch {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker is not available")
	}

	opts := options{apiKey: "integration-test"}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	return &Meilisearch{
		name:   fmt.Sprintf("dapr-meilisearch-%s-%s", sanitizeName(t.Name()), randomSuffix(t)),
		apiKey: opts.apiKey,
		port:   port,
	}
}

// Run runs the Meilisearch process.
func (m *Meilisearch) Run(t *testing.T, ctx context.Context) {
	t.Helper()

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx,
		"docker", "run", "-d", "--rm",
		"--name", m.name,
		"-p", fmt.Sprintf("127.0.0.1:%d:7700", m.port),
		"-e", "MEILI_MASTER_KEY="+m.apiKey,
		"-e", "MEILI_NO_ANALYTICS=true",
		"getmeili/meilisearch:latest",
	)
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	require.NoError(t, err, "failed to start Meilisearch container: %s", stderr.String())
	m.containerID = strings.TrimSpace(string(out))
	require.NotEmpty(t, m.containerID)

	m.waitForHealth(t, ctx)
}

// Cleanup cleans up the Meilisearch process.
func (m *Meilisearch) Cleanup(t *testing.T) {
	t.Helper()

	if m.containerID == "" {
		return
	}

	if err := exec.Command("docker", "stop", "-t", "1", m.containerID).Run(); err != nil {
		t.Logf("failed to stop Meilisearch container %s: %v", m.containerID, err)
	}
}

// Address returns the Meilisearch HTTP address.
func (m *Meilisearch) Address() string {
	return "http://" + m.Host()
}

// Host returns the Meilisearch host and port.
func (m *Meilisearch) Host() string {
	return fmt.Sprintf("127.0.0.1:%d", m.port)
}

// APIKey returns the Meilisearch master key.
func (m *Meilisearch) APIKey() string {
	return m.apiKey
}

func (m *Meilisearch) waitForHealth(t *testing.T, ctx context.Context) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	client := http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error
	for {
		available, err := m.isHealthy(ctx, &client)
		if err != nil {
			lastErr = err
		}
		if available {
			return
		}

		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err(), "Meilisearch did not become healthy within 60s; last error: %v", lastErr)
		case <-ticker.C:
		}
	}
}

func (m *Meilisearch) isHealthy(ctx context.Context, client *http.Client) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Address()+"/health", nil)
	if err != nil {
		return false, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var health struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return false, err
	}

	return health.Status == "available", nil
}

func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}

	out := strings.Trim(b.String(), "-_.")
	if out == "" {
		return "test"
	}
	return out
}

func randomSuffix(t *testing.T) string {
	t.Helper()

	b := make([]byte, 4)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}
