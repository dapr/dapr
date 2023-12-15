package lock

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/lock"
	"github.com/dapr/kit/logger"
)

const (
	compName   = "mock"
	compNameV2 = "mock/v2"
	fullName   = "lock." + compName
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	r.RegisterComponent(func(_ logger.Logger) lock.Store {
		return nil
	}, compName)
	r.RegisterComponent(func(_ logger.Logger) lock.Store {
		return nil
	}, compNameV2)
	if _, err := r.Create(fullName, "v1", ""); err != nil {
		t.Fatalf("create mock store failed: %v", err)
	}
	if _, err := r.Create(fullName, "v2", ""); err != nil {
		t.Fatalf("create mock store failed: %v", err)
	}
	if _, err := r.Create("not exists", "v1", ""); !strings.Contains(err.Error(), "couldn't find lock store") {
		t.Fatalf("create mock store failed: %v", err)
	}
}

func TestAliasing(t *testing.T) {
	const alias = "my-alias"
	r := NewRegistry()
	r.RegisterComponent(func(_ logger.Logger) lock.Store {
		return nil
	}, "", alias)
	_, err := r.Create("lock."+alias, "", "")
	require.NoError(t, err)
}
