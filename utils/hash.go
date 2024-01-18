package utils

import (
	"fmt"
	"github.com/davegardnerisme/deephash"
)

func EncodeToHash(any interface{}) string {
	hashBytes := deephash.Hash(any)

	return fmt.Sprintf("%x", hashBytes)
}
