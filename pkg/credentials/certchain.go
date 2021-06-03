package credentials

import (
	"io/ioutil"
)

const (
	// RootCertFilename is the filename that holds the root certificate.
	RootCertFilename = "ca.crt"
	// IssuerCertFilename is the filename that holds the issuer certificate.
	IssuerCertFilename = "issuer.crt"
	// IssuerKeyFilename is the filename that holds the issuer key.
	IssuerKeyFilename = "issuer.key"
)

// CertChain holds the certificate trust chain PEM values.
type CertChain struct {
	RootCA []byte
	Cert   []byte
	Key    []byte
}

// LoadFromDisk retruns a CertChain from a given directory.
func LoadFromDisk(rootCertPath, issuerCertPath, issuerKeyPath string) (*CertChain, error) {
	rootCert, err := ioutil.ReadFile(rootCertPath)
	if err != nil {
		return nil, err
	}
	cert, err := ioutil.ReadFile(issuerCertPath)
	if err != nil {
		return nil, err
	}
	key, err := ioutil.ReadFile(issuerKeyPath)
	if err != nil {
		return nil, err
	}
	return &CertChain{
		RootCA: rootCert,
		Cert:   cert,
		Key:    key,
	}, nil
}
