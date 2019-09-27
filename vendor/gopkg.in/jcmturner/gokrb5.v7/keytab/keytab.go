// Package keytab implements Kerberos keytabs: https://web.mit.edu/kerberos/krb5-devel/doc/formats/keytab_file_format.html.
package keytab

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"time"
	"unsafe"

	"gopkg.in/jcmturner/gokrb5.v7/types"
)

const (
	keytabFirstByte byte = 05
)

// Keytab struct.
type Keytab struct {
	version uint8
	Entries []entry
}

// Keytab entry struct.
type entry struct {
	Principal principal
	Timestamp time.Time
	KVNO8     uint8
	Key       types.EncryptionKey
	KVNO      uint32
}

// Keytab entry principal struct.
type principal struct {
	NumComponents int16
	Realm         string
	Components    []string
	NameType      int32
}

// New creates new, empty Keytab type.
func New() *Keytab {
	var e []entry
	return &Keytab{
		version: 0,
		Entries: e,
	}
}

// GetEncryptionKey returns the EncryptionKey from the Keytab for the newest entry with the required kvno, etype and matching principal.
func (kt *Keytab) GetEncryptionKey(princName types.PrincipalName, realm string, kvno int, etype int32) (types.EncryptionKey, error) {
	//TODO (theme: KVNO from keytab) this function should return the kvno too
	var key types.EncryptionKey
	var t time.Time
	for _, k := range kt.Entries {
		if k.Principal.Realm == realm && len(k.Principal.Components) == len(princName.NameString) &&
			k.Key.KeyType == etype &&
			(k.KVNO == uint32(kvno) || kvno == 0) &&
			k.Timestamp.After(t) {
			p := true
			for i, n := range k.Principal.Components {
				if princName.NameString[i] != n {
					p = false
					break
				}
			}
			if p {
				key = k.Key
				t = k.Timestamp
			}
		}
	}
	if len(key.KeyValue) < 1 {
		return key, fmt.Errorf("matching key not found in keytab. Looking for %v realm: %v kvno: %v etype: %v", princName.NameString, realm, kvno, etype)
	}
	return key, nil
}

// Create a new Keytab entry.
func newKeytabEntry() entry {
	var b []byte
	return entry{
		Principal: newPrincipal(),
		Timestamp: time.Time{},
		KVNO8:     0,
		Key: types.EncryptionKey{
			KeyType:  0,
			KeyValue: b,
		},
		KVNO: 0,
	}
}

// Create a new principal.
func newPrincipal() principal {
	var c []string
	return principal{
		NumComponents: 0,
		Realm:         "",
		Components:    c,
		NameType:      0,
	}
}

// Load a Keytab file into a Keytab type.
func Load(ktPath string) (*Keytab, error) {
	kt := new(Keytab)
	b, err := ioutil.ReadFile(ktPath)
	if err != nil {
		return kt, err
	}
	err = kt.Unmarshal(b)
	return kt, err
}

// Marshal keytab into byte slice
func (kt *Keytab) Marshal() ([]byte, error) {
	b := []byte{keytabFirstByte, kt.version}
	for _, e := range kt.Entries {
		eb, err := e.marshal(int(kt.version))
		if err != nil {
			return b, err
		}
		b = append(b, eb...)
	}
	return b, nil
}

// Write the keytab bytes to io.Writer.
// Returns the number of bytes written
func (kt *Keytab) Write(w io.Writer) (int, error) {
	b, err := kt.Marshal()
	if err != nil {
		return 0, fmt.Errorf("error marshaling keytab: %v", err)
	}
	return w.Write(b)
}

// Unmarshal byte slice of Keytab data into Keytab type.
func (kt *Keytab) Unmarshal(b []byte) error {
	//The first byte of the file always has the value 5
	if b[0] != keytabFirstByte {
		return errors.New("invalid keytab data. First byte does not equal 5")
	}
	//Get keytab version
	//The 2nd byte contains the version number (1 or 2)
	kt.version = b[1]
	if kt.version != 1 && kt.version != 2 {
		return errors.New("invalid keytab data. Keytab version is neither 1 nor 2")
	}
	//Version 1 of the file format uses native byte order for integer representations. Version 2 always uses big-endian byte order
	var endian binary.ByteOrder
	endian = binary.BigEndian
	if kt.version == 1 && isNativeEndianLittle() {
		endian = binary.LittleEndian
	}
	/*
		After the two-byte version indicator, the file contains a sequence of signed 32-bit record lengths followed by key records or holes.
		A positive record length indicates a valid key entry whose size is equal to or less than the record length.
		A negative length indicates a zero-filled hole whose size is the inverse of the length.
		A length of 0 indicates the end of the file.
	*/
	// n tracks position in the byte array
	n := 2
	l := readInt32(b, &n, &endian)
	for l != 0 {
		if l < 0 {
			//Zero padded so skip over
			l = l * -1
			n = n + int(l)
		} else {
			//fmt.Printf("Bytes for entry: %v\n", b[n:n+int(l)])
			eb := b[n : n+int(l)]
			n = n + int(l)
			ke := newKeytabEntry()
			// p keeps track as to where we are in the byte stream
			var p int
			parsePrincipal(eb, &p, kt, &ke, &endian)
			ke.Timestamp = readTimestamp(eb, &p, &endian)
			ke.KVNO8 = uint8(readInt8(eb, &p, &endian))
			ke.Key.KeyType = int32(readInt16(eb, &p, &endian))
			kl := int(readInt16(eb, &p, &endian))
			ke.Key.KeyValue = readBytes(eb, &p, kl, &endian)
			//The 32-bit key version overrides the 8-bit key version.
			// To determine if it is present, the implementation must check that at least 4 bytes remain in the record after the other fields are read,
			// and that the value of the 32-bit integer contained in those bytes is non-zero.
			if len(eb)-p >= 4 {
				// The 32-bit key may be present
				ke.KVNO = uint32(readInt32(eb, &p, &endian))
			}
			if ke.KVNO == 0 {
				// Handles if the value from the last 4 bytes was zero and also if there are not the 4 bytes present. Makes sense to put the same value here as KVNO8
				ke.KVNO = uint32(ke.KVNO8)
			}
			// Add the entry to the keytab
			kt.Entries = append(kt.Entries, ke)
		}
		// Check if there are still 4 bytes left to read
		if n > len(b) || len(b[n:]) < 4 {
			break
		}
		// Read the size of the next entry
		l = readInt32(b, &n, &endian)
	}
	return nil
}

func (e entry) marshal(v int) ([]byte, error) {
	var b []byte
	pb, err := e.Principal.marshal(v)
	if err != nil {
		return b, err
	}
	b = append(b, pb...)

	var endian binary.ByteOrder
	endian = binary.BigEndian
	if v == 1 && isNativeEndianLittle() {
		endian = binary.LittleEndian
	}

	t := make([]byte, 9)
	endian.PutUint32(t[0:4], uint32(e.Timestamp.Unix()))
	t[4] = e.KVNO8
	endian.PutUint16(t[5:7], uint16(e.Key.KeyType))
	endian.PutUint16(t[7:9], uint16(len(e.Key.KeyValue)))
	b = append(b, t...)

	buf := new(bytes.Buffer)
	err = binary.Write(buf, endian, e.Key.KeyValue)
	if err != nil {
		return b, err
	}
	b = append(b, buf.Bytes()...)

	t = make([]byte, 4)
	endian.PutUint32(t, e.KVNO)
	b = append(b, t...)

	// Add the length header
	t = make([]byte, 4)
	endian.PutUint32(t, uint32(len(b)))
	b = append(t, b...)
	return b, nil
}

// Parse the Keytab bytes of a principal into a Keytab entry's principal.
func parsePrincipal(b []byte, p *int, kt *Keytab, ke *entry, e *binary.ByteOrder) error {
	ke.Principal.NumComponents = readInt16(b, p, e)
	if kt.version == 1 {
		//In version 1 the number of components includes the realm. Minus 1 to make consistent with version 2
		ke.Principal.NumComponents--
	}
	lenRealm := readInt16(b, p, e)
	ke.Principal.Realm = string(readBytes(b, p, int(lenRealm), e))
	for i := 0; i < int(ke.Principal.NumComponents); i++ {
		l := readInt16(b, p, e)
		ke.Principal.Components = append(ke.Principal.Components, string(readBytes(b, p, int(l), e)))
	}
	if kt.version != 1 {
		//Name Type is omitted in version 1
		ke.Principal.NameType = readInt32(b, p, e)
	}
	return nil
}

func (p principal) marshal(v int) ([]byte, error) {
	//var b []byte
	b := make([]byte, 2)
	var endian binary.ByteOrder
	endian = binary.BigEndian
	if v == 1 && isNativeEndianLittle() {
		endian = binary.LittleEndian
	}
	endian.PutUint16(b[0:], uint16(p.NumComponents))
	realm, err := marshalString(p.Realm, v)
	if err != nil {
		return b, err
	}
	b = append(b, realm...)
	for _, c := range p.Components {
		cb, err := marshalString(c, v)
		if err != nil {
			return b, err
		}
		b = append(b, cb...)
	}
	if v != 1 {
		t := make([]byte, 4)
		endian.PutUint32(t, uint32(p.NameType))
		b = append(b, t...)
	}
	return b, nil
}

func marshalString(s string, v int) ([]byte, error) {
	sb := []byte(s)
	b := make([]byte, 2)
	var endian binary.ByteOrder
	endian = binary.BigEndian
	if v == 1 && isNativeEndianLittle() {
		endian = binary.LittleEndian
	}
	endian.PutUint16(b[0:], uint16(len(sb)))
	buf := new(bytes.Buffer)
	err := binary.Write(buf, endian, sb)
	if err != nil {
		return b, err
	}
	b = append(b, buf.Bytes()...)
	return b, err
}

// Read bytes representing a timestamp.
func readTimestamp(b []byte, p *int, e *binary.ByteOrder) time.Time {
	return time.Unix(int64(readInt32(b, p, e)), 0)
}

// Read bytes representing an eight bit integer.
func readInt8(b []byte, p *int, e *binary.ByteOrder) (i int8) {
	buf := bytes.NewBuffer(b[*p : *p+1])
	binary.Read(buf, *e, &i)
	*p++
	return
}

// Read bytes representing a sixteen bit integer.
func readInt16(b []byte, p *int, e *binary.ByteOrder) (i int16) {
	buf := bytes.NewBuffer(b[*p : *p+2])
	binary.Read(buf, *e, &i)
	*p += 2
	return
}

// Read bytes representing a thirty two bit integer.
func readInt32(b []byte, p *int, e *binary.ByteOrder) (i int32) {
	buf := bytes.NewBuffer(b[*p : *p+4])
	binary.Read(buf, *e, &i)
	*p += 4
	return
}

func readBytes(b []byte, p *int, s int, e *binary.ByteOrder) []byte {
	buf := bytes.NewBuffer(b[*p : *p+s])
	r := make([]byte, s)
	binary.Read(buf, *e, &r)
	*p += s
	return r
}

func isNativeEndianLittle() bool {
	var x = 0x012345678
	var p = unsafe.Pointer(&x)
	var bp = (*[4]byte)(p)

	var endian bool
	if 0x01 == bp[0] {
		endian = false
	} else if (0x78 & 0xff) == (bp[0] & 0xff) {
		endian = true
	} else {
		// Default to big endian
		endian = false
	}
	return endian
}
