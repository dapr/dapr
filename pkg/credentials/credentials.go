package credentials

import (
	"path/filepath"
)

// TLSCredentials holds paths for credentials.
type TLSCredentials struct {
	credentialsPath string
}

// NewTLSCredentials returns a new TLSCredentials.
func NewTLSCredentials(path string) TLSCredentials {
	return TLSCredentials{
		credentialsPath: path,
	}
}

// Path returns the directory holding the TLS credentials.
func (t *TLSCredentials) Path() string {
	return t.credentialsPath
}

// RootCertPath returns the file path for the root cert.
func (t *TLSCredentials) RootCertPath() string {
	return filepath.Join(t.credentialsPath, RootCertFilename)
}

// CertPath returns the file path for the cert.
func (t *TLSCredentials) CertPath() string {
	return filepath.Join(t.credentialsPath, IssuerCertFilename)
}

// KeyPath returns the file path for the cert key.
func (t *TLSCredentials) KeyPath() string {
	return filepath.Join(t.credentialsPath, IssuerKeyFilename)
}
