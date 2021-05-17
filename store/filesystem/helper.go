package filesystem

import (
	"crypto/sha1"
	"encoding/hex"
	"io"
	"os"

	"github.com/pkg/errors"
)

var ErrorBadContentType = errors.New("Content should be either string or []byte")

func Sha1HexDigest(content interface{}) (string, error) {
	h := sha1.New()
	switch content.(type) {
	case string:
		_, err := io.WriteString(h, content.(string))
		if err != nil {
			return "", err
		}
	case []byte:
		_, err := h.Write(content.([]byte))
		if err != nil {
			return "", err
		}
	default:
		return "", ErrorBadContentType
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func overrideFile(path string, content interface{}) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	switch content.(type) {
	case string:
		if _, err := f.WriteString(content.(string)); err != nil {
			return err
		}
	case []byte:
		if _, err := f.Write(content.([]byte)); err != nil {
			return err
		}
	default:
		return ErrorBadContentType
	}
	return f.Close()
}

func writeIfNotExist(path string, content interface{}) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	switch content.(type) {
	case []byte:
		if _, err := f.Write(content.([]byte)); err != nil {
			return err
		}
	case string:
		if _, err := f.WriteString(content.(string)); err != nil {
			return err
		}
	default:
		return ErrorBadContentType
	}
	return f.Close()
}
