// Package client provides a client library and methods for Kerberos 5 authentication.
package client

import (
	"errors"
	"fmt"
	"time"

	"gopkg.in/jcmturner/gokrb5.v7/config"
	"gopkg.in/jcmturner/gokrb5.v7/credentials"
	"gopkg.in/jcmturner/gokrb5.v7/crypto"
	"gopkg.in/jcmturner/gokrb5.v7/crypto/etype"
	"gopkg.in/jcmturner/gokrb5.v7/iana/errorcode"
	"gopkg.in/jcmturner/gokrb5.v7/iana/nametype"
	"gopkg.in/jcmturner/gokrb5.v7/keytab"
	"gopkg.in/jcmturner/gokrb5.v7/krberror"
	"gopkg.in/jcmturner/gokrb5.v7/messages"
	"gopkg.in/jcmturner/gokrb5.v7/types"
)

// Client side configuration and state.
type Client struct {
	Credentials *credentials.Credentials
	Config      *config.Config
	settings    *Settings
	sessions    *sessions
	cache       *Cache
}

// NewClientWithPassword creates a new client from a password credential.
// Set the realm to empty string to use the default realm from config.
func NewClientWithPassword(username, realm, password string, krb5conf *config.Config, settings ...func(*Settings)) *Client {
	creds := credentials.New(username, realm)
	return &Client{
		Credentials: creds.WithPassword(password),
		Config:      krb5conf,
		settings:    NewSettings(settings...),
		sessions: &sessions{
			Entries: make(map[string]*session),
		},
		cache: NewCache(),
	}
}

// NewClientWithKeytab creates a new client from a keytab credential.
func NewClientWithKeytab(username, realm string, kt *keytab.Keytab, krb5conf *config.Config, settings ...func(*Settings)) *Client {
	creds := credentials.New(username, realm)
	return &Client{
		Credentials: creds.WithKeytab(kt),
		Config:      krb5conf,
		settings:    NewSettings(settings...),
		sessions: &sessions{
			Entries: make(map[string]*session),
		},
		cache: NewCache(),
	}
}

// NewClientFromCCache create a client from a populated client cache.
//
// WARNING: A client created from CCache does not automatically renew TGTs and a failure will occur after the TGT expires.
func NewClientFromCCache(c *credentials.CCache, krb5conf *config.Config, settings ...func(*Settings)) (*Client, error) {
	cl := &Client{
		Credentials: c.GetClientCredentials(),
		Config:      krb5conf,
		settings:    NewSettings(settings...),
		sessions: &sessions{
			Entries: make(map[string]*session),
		},
		cache: NewCache(),
	}
	spn := types.PrincipalName{
		NameType:   nametype.KRB_NT_SRV_INST,
		NameString: []string{"krbtgt", c.DefaultPrincipal.Realm},
	}
	cred, ok := c.GetEntry(spn)
	if !ok {
		return cl, errors.New("TGT not found in CCache")
	}
	var tgt messages.Ticket
	err := tgt.Unmarshal(cred.Ticket)
	if err != nil {
		return cl, fmt.Errorf("TGT bytes in cache are not valid: %v", err)
	}
	cl.sessions.Entries[c.DefaultPrincipal.Realm] = &session{
		realm:      c.DefaultPrincipal.Realm,
		authTime:   cred.AuthTime,
		endTime:    cred.EndTime,
		renewTill:  cred.RenewTill,
		tgt:        tgt,
		sessionKey: cred.Key,
	}
	for _, cred := range c.GetEntries() {
		var tkt messages.Ticket
		err = tkt.Unmarshal(cred.Ticket)
		if err != nil {
			return cl, fmt.Errorf("cache entry ticket bytes are not valid: %v", err)
		}
		cl.cache.addEntry(
			tkt,
			cred.AuthTime,
			cred.StartTime,
			cred.EndTime,
			cred.RenewTill,
			cred.Key,
		)
	}
	return cl, nil
}

// Key returns the client's encryption key for the specified encryption type.
// The key can be retrieved either from the keytab or generated from the client's password.
// If the client has both a keytab and a password defined the keytab is favoured as the source for the key
// A KRBError can be passed in the event the KDC returns one of type KDC_ERR_PREAUTH_REQUIRED and is required to derive
// the key for pre-authentication from the client's password. If a KRBError is not available, pass nil to this argument.
func (cl *Client) Key(etype etype.EType, krberr *messages.KRBError) (types.EncryptionKey, error) {
	if cl.Credentials.HasKeytab() && etype != nil {
		return cl.Credentials.Keytab().GetEncryptionKey(cl.Credentials.CName(), cl.Credentials.Domain(), 0, etype.GetETypeID())
	} else if cl.Credentials.HasPassword() {
		if krberr != nil && krberr.ErrorCode == errorcode.KDC_ERR_PREAUTH_REQUIRED {
			var pas types.PADataSequence
			err := pas.Unmarshal(krberr.EData)
			if err != nil {
				return types.EncryptionKey{}, fmt.Errorf("could not get PAData from KRBError to generate key from password: %v", err)
			}
			key, _, err := crypto.GetKeyFromPassword(cl.Credentials.Password(), krberr.CName, krberr.CRealm, etype.GetETypeID(), pas)
			return key, err
		}
		key, _, err := crypto.GetKeyFromPassword(cl.Credentials.Password(), cl.Credentials.CName(), cl.Credentials.Domain(), etype.GetETypeID(), types.PADataSequence{})
		return key, err
	}
	return types.EncryptionKey{}, errors.New("credential has neither keytab or password to generate key")
}

// IsConfigured indicates if the client has the values required set.
func (cl *Client) IsConfigured() (bool, error) {
	if cl.Credentials.UserName() == "" {
		return false, errors.New("client does not have a username")
	}
	if cl.Credentials.Domain() == "" {
		return false, errors.New("client does not have a define realm")
	}
	// Client needs to have either a password, keytab or a session already (later when loading from CCache)
	if !cl.Credentials.HasPassword() && !cl.Credentials.HasKeytab() {
		authTime, _, _, _, err := cl.sessionTimes(cl.Credentials.Domain())
		if err != nil || authTime.IsZero() {
			return false, errors.New("client has neither a keytab nor a password set and no session")
		}
	}
	if !cl.Config.LibDefaults.DNSLookupKDC {
		for _, r := range cl.Config.Realms {
			if r.Realm == cl.Credentials.Domain() {
				if len(r.KDC) > 0 {
					return true, nil
				}
				return false, errors.New("client krb5 config does not have any defined KDCs for the default realm")
			}
		}
	}
	return true, nil
}

// Login the client with the KDC via an AS exchange.
func (cl *Client) Login() error {
	if ok, err := cl.IsConfigured(); !ok {
		return err
	}
	if !cl.Credentials.HasPassword() && !cl.Credentials.HasKeytab() {
		_, endTime, _, _, err := cl.sessionTimes(cl.Credentials.Domain())
		if err != nil {
			return krberror.Errorf(err, krberror.KRBMsgError, "no user credentials available and error getting any existing session")
		}
		if time.Now().UTC().After(endTime) {
			return krberror.NewKrberror(krberror.KRBMsgError, "cannot login, no user credentials available and no valid existing session")
		}
		// no credentials but there is a session with tgt already
		return nil
	}
	ASReq, err := messages.NewASReqForTGT(cl.Credentials.Domain(), cl.Config, cl.Credentials.CName())
	if err != nil {
		return krberror.Errorf(err, krberror.KRBMsgError, "error generating new AS_REQ")
	}
	ASRep, err := cl.ASExchange(cl.Credentials.Domain(), ASReq, 0)
	if err != nil {
		return err
	}
	cl.addSession(ASRep.Ticket, ASRep.DecryptedEncPart)
	return nil
}

// realmLogin obtains or renews a TGT and establishes a session for the realm specified.
func (cl *Client) realmLogin(realm string) error {
	if realm == cl.Credentials.Domain() {
		return cl.Login()
	}
	_, endTime, _, _, err := cl.sessionTimes(cl.Credentials.Domain())
	if err != nil || time.Now().UTC().After(endTime) {
		err := cl.Login()
		if err != nil {
			return fmt.Errorf("could not get valid TGT for client's realm: %v", err)
		}
	}
	tgt, skey, err := cl.sessionTGT(cl.Credentials.Domain())
	if err != nil {
		return err
	}

	spn := types.PrincipalName{
		NameType:   nametype.KRB_NT_SRV_INST,
		NameString: []string{"krbtgt", realm},
	}

	_, tgsRep, err := cl.TGSREQGenerateAndExchange(spn, cl.Credentials.Domain(), tgt, skey, false)
	if err != nil {
		return err
	}
	cl.addSession(tgsRep.Ticket, tgsRep.DecryptedEncPart)

	return nil
}

// Destroy stops the auto-renewal of all sessions and removes the sessions and cache entries from the client.
func (cl *Client) Destroy() {
	creds := credentials.New("", "")
	cl.sessions.destroy()
	cl.cache.clear()
	cl.Credentials = creds
	cl.Log("client destroyed")
}
