package utils

import (
	"bytes"
	"crypto/sha256"

	"encoding/gob"
	"fmt"
)

func EncodeToHash(any interface{}) string {
	// encode struct to bytes
	var buffer bytes.Buffer
	gob.NewEncoder(&buffer).Encode(any)
	// create hash
	// https://pkg.go.dev/crypto/sha256#example-New
	first := sha256.New()
	first.Write(buffer.Bytes())
	return fmt.Sprintf("%x", first.Sum(nil))
}
