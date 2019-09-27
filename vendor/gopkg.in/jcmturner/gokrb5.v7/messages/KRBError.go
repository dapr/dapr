// Package messages implements Kerberos 5 message types and methods.
package messages

import (
	"fmt"
	"time"

	"github.com/jcmturner/gofork/encoding/asn1"
	"gopkg.in/jcmturner/gokrb5.v7/iana"
	"gopkg.in/jcmturner/gokrb5.v7/iana/asnAppTag"
	"gopkg.in/jcmturner/gokrb5.v7/iana/errorcode"
	"gopkg.in/jcmturner/gokrb5.v7/iana/msgtype"
	"gopkg.in/jcmturner/gokrb5.v7/krberror"
	"gopkg.in/jcmturner/gokrb5.v7/types"
)

// KRBError implements RFC 4120 KRB_ERROR: https://tools.ietf.org/html/rfc4120#section-5.9.1.
type KRBError struct {
	PVNO      int                 `asn1:"explicit,tag:0"`
	MsgType   int                 `asn1:"explicit,tag:1"`
	CTime     time.Time           `asn1:"generalized,optional,explicit,tag:2"`
	Cusec     int                 `asn1:"optional,explicit,tag:3"`
	STime     time.Time           `asn1:"generalized,explicit,tag:4"`
	Susec     int                 `asn1:"explicit,tag:5"`
	ErrorCode int32               `asn1:"explicit,tag:6"`
	CRealm    string              `asn1:"generalstring,optional,explicit,tag:7"`
	CName     types.PrincipalName `asn1:"optional,explicit,tag:8"`
	Realm     string              `asn1:"generalstring,explicit,tag:9"`
	SName     types.PrincipalName `asn1:"explicit,tag:10"`
	EText     string              `asn1:"generalstring,optional,explicit,tag:11"`
	EData     []byte              `asn1:"optional,explicit,tag:12"`
}

// NewKRBError creates a new KRBError.
func NewKRBError(sname types.PrincipalName, realm string, code int32, etext string) KRBError {
	t := time.Now().UTC()
	return KRBError{
		PVNO:      iana.PVNO,
		MsgType:   msgtype.KRB_ERROR,
		STime:     t,
		Susec:     int((t.UnixNano() / int64(time.Microsecond)) - (t.Unix() * 1e6)),
		ErrorCode: code,
		SName:     sname,
		Realm:     realm,
		EText:     etext,
	}
}

// Unmarshal bytes b into the KRBError struct.
func (k *KRBError) Unmarshal(b []byte) error {
	_, err := asn1.UnmarshalWithParams(b, k, fmt.Sprintf("application,explicit,tag:%v", asnAppTag.KRBError))
	if err != nil {
		return krberror.Errorf(err, krberror.EncodingError, "KRB_ERROR unmarshal error")
	}
	expectedMsgType := msgtype.KRB_ERROR
	if k.MsgType != expectedMsgType {
		return krberror.NewErrorf(krberror.KRBMsgError, "message ID does not indicate a KRB_ERROR. Expected: %v; Actual: %v", expectedMsgType, k.MsgType)
	}
	return nil
}

// Error method implementing error interface on KRBError struct.
func (k KRBError) Error() string {
	etxt := fmt.Sprintf("KRB Error: %s", errorcode.Lookup(k.ErrorCode))
	if k.EText != "" {
		etxt = fmt.Sprintf("%s - %s", etxt, k.EText)
	}
	return etxt
}

func processUnmarshalReplyError(b []byte, err error) error {
	switch err.(type) {
	case asn1.StructuralError:
		var krberr KRBError
		tmperr := krberr.Unmarshal(b)
		if tmperr != nil {
			return krberror.Errorf(err, krberror.EncodingError, "failed to unmarshal KDC's reply")
		}
		return krberr
	default:
		return krberror.Errorf(err, krberror.EncodingError, "failed to unmarshal KDC's reply")
	}
}
