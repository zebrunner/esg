package utils

import (
	"github.com/davegardnerisme/deephash"
	"fmt"
)

func EncodeToHash(any interface{}) string {
	hashBytes := deephash.Hash(any)

	return fmt.Sprintf("%x", hashBytes)
}
