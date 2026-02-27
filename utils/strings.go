package utils

import (
	"fmt"
	"strings"
)

type TagMap map[string]string

func (t *TagMap) String() string {
	if t == nil {
		return ""
	}
	pairs := make([]string, 0, len(*t))
	for k, v := range *t {
		pairs = append(pairs, k+"="+v)
	}
	return strings.Join(pairs, ",")
}

func (t *TagMap) Set(value string) error {
	if *t == nil {
		*t = make(TagMap)
	}
	for _, pair := range strings.Split(value, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			return fmt.Errorf("invalid tag format %q, expected key=value", pair)
		}
		(*t)[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return nil
}
