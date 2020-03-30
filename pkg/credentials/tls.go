package credentials

import (
	"crypto/tls"
	"crypto/x509"
)

func TLSConfigFromCertAndKey(certPem, keyPem []byte, serverName string, rootCA *x509.CertPool) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPem, keyPem)
	if err != nil {
		return nil, err
	}

	config := &tls.Config{
		InsecureSkipVerify: false,
		RootCAs:            rootCA,
		ServerName:         serverName,
		Certificates:       []tls.Certificate{cert},
	}

	return config, nil
}
