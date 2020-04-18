package credentials

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var TestCACert = `-----BEGIN CERTIFICATE-----
MIIBjjCCATOgAwIBAgIQdZeGNuAHZhXSmb37Pnx2QzAKBggqhkjOPQQDAjAYMRYw
FAYDVQQDEw1jbHVzdGVyLmxvY2FsMB4XDTIwMDIwMTAwMzUzNFoXDTMwMDEyOTAw
MzUzNFowGDEWMBQGA1UEAxMNY2x1c3Rlci5sb2NhbDBZMBMGByqGSM49AgEGCCqG
SM49AwEHA0IABAeMFRst4JhcFpebfgEs1MvJdD7h5QkCbLwChRHVEUoaDqd1aYjm
bX5SuNBXz5TBEhHfTV3Objh6LQ2N+CBoCeOjXzBdMA4GA1UdDwEB/wQEAwIBBjAS
BgNVHRMBAf8ECDAGAQH/AgEBMB0GA1UdDgQWBBRBWthv5ZQ3vALl2zXWwAXSmZ+m
qTAYBgNVHREEETAPgg1jbHVzdGVyLmxvY2FsMAoGCCqGSM49BAMCA0kAMEYCIQDN
rQNOck4ENOhmLROE/wqH0MKGjE6P8yzesgnp9fQI3AIhAJaVPrZloxl1dWCgmNWo
Iklq0JnMgJU7nS+VpVvlgBN8
-----END CERTIFICATE-----`

var TestCert = `-----BEGIN CERTIFICATE-----
MIIBXDCCAQOgAwIBAgIRALFHPINM7m/sHbH775ZjtGYwCgYIKoZIzj0EAwIwKjEX
MBUGA1UEChMOZGFwci5pby9zZW50cnkxDzANBgNVBAMTBnNlbnRyeTAeFw0yMDAy
MTEwMDQ1NThaFw0yMTAyMTAwMDQ1NThaMBExDzANBgNVBAMTBnNlbnRyeTBZMBMG
ByqGSM49AgEGCCqGSM49AwEHA0IABK4QF+h1jJDBnXcWc4lwewgq+4fcb7Ud6SSx
FEiiaOTSsZfb/IY0T8VGLHSalc1jFlCfD8mNuhjx9QTgR6QPRwGjIzAhMA4GA1Ud
DwEB/wQEAwIBBjAPBgNVHRMBAf8EBTADAQH/MAoGCCqGSM49BAMCA0cAMEQCIBk1
k8Cu51NLvo2esE4YvA65fzjYIo7hC7JjQJ107QARAiAnbsZu/InV17eJWTohNSPB
hIzOUyB1HWO0KobCoOPGPQ==
-----END CERTIFICATE-----`

var TestKey = `-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIIqwzdYX+5OM7qeU3sWCApUdyK35q11i3ma1JmcRHxcJoAoGCCqGSM49
AwEHoUQDQgAErhAX6HWMkMGddxZziXB7CCr7h9xvtR3pJLEUSKJo5NKxl9v8hjRP
xUYsdJqVzWMWUJ8PyY26GPH1BOBHpA9HAQ==
-----END EC PRIVATE KEY-----`

func TestTLSConfigFromCertAndKey(t *testing.T) {
	t.Run("invalid cert", func(t *testing.T) {
		conf, err := TLSConfigFromCertAndKey(nil, []byte(TestKey), "server", nil)
		assert.NotNil(t, err)
		assert.Nil(t, conf)
	})

	t.Run("invalid key", func(t *testing.T) {
		conf, err := TLSConfigFromCertAndKey([]byte(TestCert), nil, "server", nil)
		assert.NotNil(t, err)
		assert.Nil(t, conf)
	})

	t.Run("valid cert and keys", func(t *testing.T) {
		conf, err := TLSConfigFromCertAndKey([]byte(TestCert), []byte(TestKey), "server", nil)
		assert.Nil(t, err)
		assert.NotNil(t, conf)
	})
}
