package utils

import "strings"

// StringSlice implements flag.Value and can hold a slice of strings
// separated by `;`
type StringSlice []string

func (s *StringSlice) String() string {
	return strings.Join(*s, ",")
}

func (s *StringSlice) Set(value string) error {
	*s = strings.Split(value, ",")
	for i := range *s {
		(*s)[i] = strings.TrimSpace((*s)[i])
	}
	return nil
}

func (s StringSlice) ToStringSlice() []*string {
	out := make([]*string, len(s))
	for i := range s {
		str := s[i] // avoid taking address of loop variable
		out[i] = &str
	}
	return out
}
