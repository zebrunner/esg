package cache

import (
	"sync"
)

var (
	cache = &RevisionsCache{}
)

type RevisionsCache struct {
	sync.RWMutex
	Cache map[string]int64
}

func GetCache() *RevisionsCache {
	return cache
}
