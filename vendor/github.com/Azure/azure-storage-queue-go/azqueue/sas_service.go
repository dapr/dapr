package azqueue

import (
	"fmt"
	"strings"
	"time"
)

// QueueSASSignatureValues is used to generate a Shared Access Signature (SAS) for an Azure Storage queue.
type QueueSASSignatureValues struct {
	Version     string      `param:"sv"`  // If not specified, this defaults to SASVersion
	Protocol    SASProtocol `param:"spr"` // See the SASProtocol* constants
	StartTime   time.Time   `param:"st"`  // Not specified if IsZero
	ExpiryTime  time.Time   `param:"se"`  // Not specified if IsZero
	Permissions string      `param:"sp"`  // Create by initializing a QueueSASPermissions and then call String()
	IPRange     IPRange     `param:"sip"`
	Identifier  string      `param:"si"`
	QueueName   string
}

// NewSASQueryParameters uses an account's shared key credential to sign this signature values to produce
// the proper SAS query parameters.
func (v QueueSASSignatureValues) NewSASQueryParameters(sharedKeyCredential *SharedKeyCredential) SASQueryParameters {
	if v.Version == "" {
		v.Version = SASVersion
	}
	startTime, expiryTime := FormatTimesForSASSigning(v.StartTime, v.ExpiryTime)

	// String to sign: http://msdn.microsoft.com/en-us/library/azure/dn140255.aspx
	stringToSign := strings.Join([]string{
		v.Permissions,
		startTime,
		expiryTime,
		getCanonicalName(sharedKeyCredential.AccountName(), v.QueueName),
		v.Identifier,
		v.IPRange.String(),
		string(v.Protocol),
		v.Version},
		"\n")
	signature := sharedKeyCredential.ComputeHMACSHA256(stringToSign)

	p := SASQueryParameters{
		// Common SAS parameters
		version:     v.Version,
		protocol:    v.Protocol,
		startTime:   v.StartTime,
		expiryTime:  v.ExpiryTime,
		permissions: v.Permissions,
		ipRange:     v.IPRange,

		// Queue-specific SAS parameters
		resource:   "q",
		identifier: v.Identifier,

		// Calculated SAS signature
		signature: signature,
	}
	return p
}

// getCanonicalName computes the canonical name for a queue resource for SAS signing.
func getCanonicalName(account string, queueName string) string {
	elements := []string{"/queue/", account, "/", queueName}
	return strings.Join(elements, "")
}

// The QueueSASPermissions type simplifies creating the permissions string for an Azure Storage queue SAS.
// Initialize an instance of this type and then call its String method to set QueueSASSignatureValues's Permissions field.
type QueueSASPermissions struct {
	Read, Add, Update, Process bool
}

// String produces the SAS permissions string for an Azure Storage queue.
// Call this method to set QueueSASSignatureValues's Permissions field.
func (p QueueSASPermissions) String() string {
	var b strings.Builder
	if p.Read {
		b.WriteRune('r')
	}
	if p.Add {
		b.WriteRune('a')
	}
	if p.Update {
		b.WriteRune('u')
	}
	if p.Process {
		b.WriteRune('p')
	}
	return b.String()
}

// Parse initializes the QueueSASPermissions's fields from a string.
func (p *QueueSASPermissions) Parse(s string) error {
	*p = QueueSASPermissions{} // Clear the flags
	for _, r := range s {
		switch r {
		case 'r':
			p.Read = true
		case 'a':
			p.Add = true
		case 'u':
			p.Update = true
		case 'p':
			p.Process = true
		default:
			return fmt.Errorf("Invalid permission: '%v'", r)
		}
	}
	return nil
}
