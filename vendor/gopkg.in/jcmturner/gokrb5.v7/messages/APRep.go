package messages

import (
	"fmt"
	"time"

	"github.com/jcmturner/gofork/encoding/asn1"
	"gopkg.in/jcmturner/gokrb5.v7/iana/asnAppTag"
	"gopkg.in/jcmturner/gokrb5.v7/iana/msgtype"
	"gopkg.in/jcmturner/gokrb5.v7/krberror"
	"gopkg.in/jcmturner/gokrb5.v7/types"
)

/*
AP-REP          ::= [APPLICATION 15] SEQUENCE {
pvno            [0] INTEGER (5),
msg-type        [1] INTEGER (15),
enc-part        [2] EncryptedData -- EncAPRepPart
}

EncAPRepPart    ::= [APPLICATION 27] SEQUENCE {
        ctime           [0] KerberosTime,
        cusec           [1] Microseconds,
        subkey          [2] EncryptionKey OPTIONAL,
        seq-number      [3] UInt32 OPTIONAL
}
*/

// APRep implements RFC 4120 KRB_AP_REP: https://tools.ietf.org/html/rfc4120#section-5.5.2.
type APRep struct {
	PVNO    int                 `asn1:"explicit,tag:0"`
	MsgType int                 `asn1:"explicit,tag:1"`
	EncPart types.EncryptedData `asn1:"explicit,tag:2"`
}

// EncAPRepPart is the encrypted part of KRB_AP_REP.
type EncAPRepPart struct {
	CTime          time.Time           `asn1:"generalized,explicit,tag:0"`
	Cusec          int                 `asn1:"explicit,tag:1"`
	Subkey         types.EncryptionKey `asn1:"optional,explicit,tag:2"`
	SequenceNumber int64               `asn1:"optional,explicit,tag:3"`
}

// Unmarshal bytes b into the APRep struct.
func (a *APRep) Unmarshal(b []byte) error {
	_, err := asn1.UnmarshalWithParams(b, a, fmt.Sprintf("application,explicit,tag:%v", asnAppTag.APREP))
	if err != nil {
		return processUnmarshalReplyError(b, err)
	}
	expectedMsgType := msgtype.KRB_AP_REP
	if a.MsgType != expectedMsgType {
		return krberror.NewErrorf(krberror.KRBMsgError, "message ID does not indicate a KRB_AP_REP. Expected: %v; Actual: %v", expectedMsgType, a.MsgType)
	}
	return nil
}

// Unmarshal bytes b into the APRep encrypted part struct.
func (a *EncAPRepPart) Unmarshal(b []byte) error {
	_, err := asn1.UnmarshalWithParams(b, a, fmt.Sprintf("application,explicit,tag:%v", asnAppTag.EncAPRepPart))
	if err != nil {
		return krberror.Errorf(err, krberror.EncodingError, "AP_REP unmarshal error")
	}
	return nil
}
