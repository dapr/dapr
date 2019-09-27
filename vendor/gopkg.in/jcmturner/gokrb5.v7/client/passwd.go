package client

import (
	"fmt"
	"net"

	"gopkg.in/jcmturner/gokrb5.v7/kadmin"
	"gopkg.in/jcmturner/gokrb5.v7/messages"
)

// Kpasswd server response codes.
const (
	KRB5_KPASSWD_SUCCESS             = 0
	KRB5_KPASSWD_MALFORMED           = 1
	KRB5_KPASSWD_HARDERROR           = 2
	KRB5_KPASSWD_AUTHERROR           = 3
	KRB5_KPASSWD_SOFTERROR           = 4
	KRB5_KPASSWD_ACCESSDENIED        = 5
	KRB5_KPASSWD_BAD_VERSION         = 6
	KRB5_KPASSWD_INITIAL_FLAG_NEEDED = 7
)

// ChangePasswd changes the password of the client to the value provided.
func (cl *Client) ChangePasswd(newPasswd string) (bool, error) {
	ASReq, err := messages.NewASReqForChgPasswd(cl.Credentials.Domain(), cl.Config, cl.Credentials.CName())
	if err != nil {
		return false, err
	}
	ASRep, err := cl.ASExchange(cl.Credentials.Domain(), ASReq, 0)
	if err != nil {
		return false, err
	}

	msg, key, err := kadmin.ChangePasswdMsg(cl.Credentials.CName(), cl.Credentials.Domain(), newPasswd, ASRep.Ticket, ASRep.DecryptedEncPart.Key)
	if err != nil {
		return false, err
	}
	r, err := cl.sendToKPasswd(msg)
	if err != nil {
		return false, err
	}
	err = r.Decrypt(key)
	if err != nil {
		return false, err
	}
	if r.ResultCode != KRB5_KPASSWD_SUCCESS {
		return false, fmt.Errorf("error response from kdamin: %s", r.Result)
	}
	cl.Credentials.WithPassword(newPasswd)
	return true, nil
}

func (cl *Client) sendToKPasswd(msg kadmin.Request) (r kadmin.Reply, err error) {
	_, kps, err := cl.Config.GetKpasswdServers(cl.Credentials.Domain(), true)
	if err != nil {
		return
	}
	addr := kps[1]
	b, err := msg.Marshal()
	if err != nil {
		return
	}
	if len(b) <= cl.Config.LibDefaults.UDPPreferenceLimit {
		return cl.sendKPasswdUDP(b, addr)
	}
	return cl.sendKPasswdTCP(b, addr)
}

func (cl *Client) sendKPasswdTCP(b []byte, kadmindAddr string) (r kadmin.Reply, err error) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", kadmindAddr)
	if err != nil {
		return
	}
	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		return
	}
	rb, err := cl.sendTCP(conn, b)
	err = r.Unmarshal(rb)
	return
}

func (cl *Client) sendKPasswdUDP(b []byte, kadmindAddr string) (r kadmin.Reply, err error) {
	udpAddr, err := net.ResolveUDPAddr("udp", kadmindAddr)
	if err != nil {
		return
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return
	}
	rb, err := cl.sendUDP(conn, b)
	err = r.Unmarshal(rb)
	return
}
