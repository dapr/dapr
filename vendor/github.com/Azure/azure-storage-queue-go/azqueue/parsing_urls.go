package azqueue

import (
	"errors"
	"net/url"
	"strings"
)

// A QueueURLParts object represents the components that make up an Azure Storage Queue URL. You parse an
// existing URL into its parts by calling NewQueueURLParts(). You construct a URL from parts by calling URL().
// NOTE: Changing any SAS-related field requires computing a new SAS signature.
type QueueURLParts struct {
	Scheme         string // Ex: "https://"
	Host           string // Ex: "account.queue.core.windows.net"
	QueueName      string // "" if no queue name
	Messages       bool   // true if "/messages" was/should be in URL
	MessageID      MessageID
	SAS            SASQueryParameters
	UnparsedParams string
}

// NewQueueURLParts parses a URL initializing QueueURLParts' fields including any SAS-related query parameters. Any other
// query parameters remain in the UnparsedParams field. This method overwrites all fields in the QueueURLParts object.
func NewQueueURLParts(u url.URL) QueueURLParts {
	up := QueueURLParts{
		Scheme: u.Scheme,
		Host:   u.Host,
	}

	// Full path example: /queue-name/messages/messageID
	// Find the queue name (if any)
	if u.Path != "" {
		path := u.Path
		if path[0] == '/' {
			path = path[1:] // If path starts with a slash, remove it
		}

		components := strings.Split(path, "/")
		if len(components) > 0 {
			up.QueueName = components[0]
			if len(components) > 1 {
				up.Messages = true
				if len(components) > 2 {
					up.MessageID = MessageID(components[2])
				}
			}
		}
	}

	// Convert the query parameters to a case-sensitive map & trim whitsapce
	paramsMap := u.Query()
	up.SAS = newSASQueryParameters(paramsMap, true)
	up.UnparsedParams = paramsMap.Encode()
	return up
}

// URL returns a URL object whose fields are initialized from the QueueURLParts fields. The URL's RawQuery
// field contains the SAS and unparsed query parameters.
func (up QueueURLParts) URL() (url.URL, error) {
	if up.MessageID != "" && !up.Messages || up.QueueName == "" {
		return url.URL{}, errors.New("can't produce a URL with a messageID but without a queue name or Messages")
	}
	if up.MessageID == "" && up.Messages && up.QueueName == "" {
		return url.URL{}, errors.New("can't produce a URL with Messages but without a queue name ")
	}

	path := ""
	// Concatenate queue name (if it exists)
	if up.QueueName != "" {
		path += "/" + up.QueueName
		if up.Messages {
			path += "/messages"
		}
		if up.MessageID != "" {
			path += "/" + string(up.MessageID)
		}
	}

	rawQuery := up.UnparsedParams

	sas := up.SAS.Encode()
	if sas != "" {
		if len(rawQuery) > 0 {
			rawQuery += "&"
		}
		rawQuery += sas
	}
	u := url.URL{
		Scheme:   up.Scheme,
		Host:     up.Host,
		Path:     path,
		RawQuery: rawQuery,
	}
	return u, nil
}
