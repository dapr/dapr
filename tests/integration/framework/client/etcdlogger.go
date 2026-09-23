/*
Copyright 2026 The Dapr Authors
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

package client

import (
	"testing"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/dapr/dapr/tests/integration/framework/iowriter"
)

// EtcdLogger returns a logger for an in-process etcd client which writes into
// the test's captured output.
//
// Left unset, clientv3 builds its own logger straight to stderr, which `go
// test` cannot attribute to a test and so prints in the middle of a run:
//
//	{"level":"warn",...,"logger":"etcd-client","msg":"retrying of unary invoker failed",...}
func EtcdLogger(t *testing.T) *zap.Logger {
	t.Helper()

	return zap.New(zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(iowriter.New(t, "etcd-client")),
		zapcore.WarnLevel,
	))
}

// WithEtcdLogger returns cfg with a logger attached, unless the caller already
// chose one.
func WithEtcdLogger(t *testing.T, cfg clientv3.Config) clientv3.Config {
	t.Helper()

	if cfg.Logger == nil {
		cfg.Logger = EtcdLogger(t)
	}

	return cfg
}
