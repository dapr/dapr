package amqp

// SASL Codes
const (
	codeSASLOK      saslCode = iota // Connection authentication succeeded.
	codeSASLAuth                    // Connection authentication failed due to an unspecified problem with the supplied credentials.
	codeSASLSys                     // Connection authentication failed due to a system error.
	codeSASLSysPerm                 // Connection authentication failed due to a system error that is unlikely to be corrected without intervention.
	codeSASLSysTemp                 // Connection authentication failed due to a transient system error.
)

// SASL Mechanisms
const (
	saslMechanismPLAIN     symbol = "PLAIN"
	saslMechanismANONYMOUS symbol = "ANONYMOUS"
)

type saslCode uint8

func (s saslCode) marshal(wr *buffer) error {
	return marshal(wr, uint8(s))
}

func (s *saslCode) unmarshal(r *buffer) error {
	n, err := readUbyte(r)
	*s = saslCode(n)
	return err
}

// ConnSASLPlain enables SASL PLAIN authentication for the connection.
//
// SASL PLAIN transmits credentials in plain text and should only be used
// on TLS/SSL enabled connection.
func ConnSASLPlain(username, password string) ConnOption {
	// TODO: how widely used is hostname? should it be supported
	return func(c *conn) error {
		// make handlers map if no other mechanism has
		if c.saslHandlers == nil {
			c.saslHandlers = make(map[symbol]stateFunc)
		}

		// add the handler the the map
		c.saslHandlers[saslMechanismPLAIN] = func() stateFunc {
			// send saslInit with PLAIN payload
			c.err = c.writeFrame(frame{
				type_: frameTypeSASL,
				body: &saslInit{
					Mechanism:       "PLAIN",
					InitialResponse: []byte("\x00" + username + "\x00" + password),
					Hostname:        "",
				},
			})
			if c.err != nil {
				return nil
			}

			// go to c.saslOutcome to handle the server response
			return c.saslOutcome
		}
		return nil
	}
}

// ConnSASLAnonymous enables SASL ANONYMOUS authentication for the connection.
func ConnSASLAnonymous() ConnOption {
	return func(c *conn) error {
		// make handlers map if no other mechanism has
		if c.saslHandlers == nil {
			c.saslHandlers = make(map[symbol]stateFunc)
		}

		// add the handler the the map
		c.saslHandlers[saslMechanismANONYMOUS] = func() stateFunc {
			c.err = c.writeFrame(frame{
				type_: frameTypeSASL,
				body: &saslInit{
					Mechanism:       saslMechanismANONYMOUS,
					InitialResponse: []byte("anonymous"),
				},
			})
			if c.err != nil {
				return nil
			}

			// go to c.saslOutcome to handle the server response
			return c.saslOutcome
		}
		return nil
	}
}
