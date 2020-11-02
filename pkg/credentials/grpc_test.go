package credentials

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServerOptions(t *testing.T) {
	t.Run("valid certs", func(t *testing.T) {
		chain := &CertChain{
			RootCA: []byte(TestCACert),
			Cert:   []byte(TestCert),
			Key:    []byte(TestKey),
		}
		opts, err := GetServerOptions(chain)
		assert.Nil(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("invalid certs", func(t *testing.T) {
		chain := &CertChain{
			RootCA: []byte(nil),
			Cert:   []byte(nil),
			Key:    []byte(nil),
		}
		opts, err := GetServerOptions(chain)
		assert.Nil(t, err)
		assert.NotNil(t, opts)
		assert.Len(t, opts, 0)
	})
}

func TestClientOptions(t *testing.T) {
	t.Run("valid certs", func(t *testing.T) {
		chain := &CertChain{
			RootCA: []byte(TestCACert),
			Cert:   []byte(TestCert),
			Key:    []byte(TestKey),
		}
		opts, err := GetClientOptions(chain, "")
		assert.Nil(t, err)
		assert.Len(t, opts, 1)
	})

	t.Run("invalid certs", func(t *testing.T) {
		chain := &CertChain{
			RootCA: []byte(nil),
			Cert:   []byte(nil),
			Key:    []byte(nil),
		}
		opts, err := GetClientOptions(chain, "")
		assert.NotNil(t, err)
		assert.Nil(t, opts)
	})
}
