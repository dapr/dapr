package documentdb

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
        "errors"
)

func authorize(str, key string) (string, error) {
	var ret string
	enc := base64.StdEncoding
	salt, err := enc.DecodeString(key)

	if err != nil {
		if _, ok := err.(base64.CorruptInputError); ok {
                        err = errors.New("base64 input is corrupt, check CosmosDB key.")
			return ret, err
		}
		return ret, err
	}
	hmac := hmac.New(sha256.New, salt)
	hmac.Write([]byte(str))
	b := hmac.Sum(nil)

	ret = enc.EncodeToString(b)
	return ret, nil
}
