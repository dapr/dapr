// Package common provides encryption methods common across encryption types
package common

import (
	"bytes"
	"crypto/hmac"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"gopkg.in/jcmturner/gokrb5.v7/crypto/etype"
)

// ZeroPad pads bytes with zeros to nearest multiple of message size m.
func ZeroPad(b []byte, m int) ([]byte, error) {
	if m <= 0 {
		return nil, errors.New("Invalid message block size when padding")
	}
	if b == nil || len(b) == 0 {
		return nil, errors.New("Data not valid to pad: Zero size")
	}
	if l := len(b) % m; l != 0 {
		n := m - l
		z := make([]byte, n)
		b = append(b, z...)
	}
	return b, nil
}

// PKCS7Pad pads bytes according to RFC 2315 to nearest multiple of message size m.
func PKCS7Pad(b []byte, m int) ([]byte, error) {
	if m <= 0 {
		return nil, errors.New("Invalid message block size when padding")
	}
	if b == nil || len(b) == 0 {
		return nil, errors.New("Data not valid to pad: Zero size")
	}
	n := m - (len(b) % m)
	pb := make([]byte, len(b)+n)
	copy(pb, b)
	copy(pb[len(b):], bytes.Repeat([]byte{byte(n)}, n))
	return pb, nil
}

// PKCS7Unpad removes RFC 2315 padding from byes where message size is m.
func PKCS7Unpad(b []byte, m int) ([]byte, error) {
	if m <= 0 {
		return nil, errors.New("invalid message block size when unpadding")
	}
	if b == nil || len(b) == 0 {
		return nil, errors.New("padded data not valid: Zero size")
	}
	if len(b)%m != 0 {
		return nil, errors.New("padded data not valid: Not multiple of message block size")
	}
	c := b[len(b)-1]
	n := int(c)
	if n == 0 || n > len(b) {
		return nil, errors.New("padded data not valid: Data may not have been padded")
	}
	for i := 0; i < n; i++ {
		if b[len(b)-n+i] != c {
			return nil, errors.New("padded data not valid")
		}
	}
	return b[:len(b)-n], nil
}

// GetHash generates the keyed hash value according to the etype's hash function.
func GetHash(pt, key []byte, usage []byte, etype etype.EType) ([]byte, error) {
	k, err := etype.DeriveKey(key, usage)
	if err != nil {
		return nil, fmt.Errorf("unable to derive key for checksum: %v", err)
	}
	mac := hmac.New(etype.GetHashFunc(), k)
	p := make([]byte, len(pt))
	copy(p, pt)
	mac.Write(p)
	return mac.Sum(nil)[:etype.GetHMACBitLength()/8], nil
}

// GetChecksumHash returns a keyed checksum hash of the bytes provided.
func GetChecksumHash(b, key []byte, usage uint32, etype etype.EType) ([]byte, error) {
	return GetHash(b, key, GetUsageKc(usage), etype)
}

// GetIntegrityHash returns a keyed integrity hash of the bytes provided.
func GetIntegrityHash(b, key []byte, usage uint32, etype etype.EType) ([]byte, error) {
	return GetHash(b, key, GetUsageKi(usage), etype)
}

// VerifyChecksum compares the checksum of the msg bytes is the same as the checksum provided.
func VerifyChecksum(key, chksum, msg []byte, usage uint32, etype etype.EType) bool {
	//The ciphertext output is the concatenation of the output of the basic
	//encryption function E and a (possibly truncated) HMAC using the
	//specified hash function H, both applied to the plaintext with a
	//random confounder prefix and sufficient padding to bring it to a
	//multiple of the message block size.  When the HMAC is computed, the
	//key is used in the protocol key form.
	expectedMAC, _ := GetChecksumHash(msg, key, usage, etype)
	return hmac.Equal(chksum, expectedMAC)
}

// GetUsageKc returns the checksum key usage value for the usage number un.
//
// RFC 3961: The "well-known constant" used for the DK function is the key usage number, expressed as four octets in big-endian order, followed by one octet indicated below.
//
// Kc = DK(base-key, usage | 0x99);
func GetUsageKc(un uint32) []byte {
	return getUsage(un, 0x99)
}

// GetUsageKe returns the encryption key usage value for the usage number un
//
// RFC 3961: The "well-known constant" used for the DK function is the key usage number, expressed as four octets in big-endian order, followed by one octet indicated below.
//
// Ke = DK(base-key, usage | 0xAA);
func GetUsageKe(un uint32) []byte {
	return getUsage(un, 0xAA)
}

// GetUsageKi returns the integrity key usage value for the usage number un
//
// RFC 3961: The "well-known constant" used for the DK function is the key usage number, expressed as four octets in big-endian order, followed by one octet indicated below.
//
// Ki = DK(base-key, usage | 0x55);
func GetUsageKi(un uint32) []byte {
	return getUsage(un, 0x55)
}

func getUsage(un uint32, o byte) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, un)
	return append(buf.Bytes(), o)
}

// IterationsToS2Kparams converts the number of iterations as an integer to a string representation.
func IterationsToS2Kparams(i uint32) string {
	b := make([]byte, 4, 4)
	binary.BigEndian.PutUint32(b, i)
	return hex.EncodeToString(b)
}
