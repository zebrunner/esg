package utils

import (
	"net"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

type RetryingTransport struct {
	Base    http.RoundTripper
	Retries int
	Delay   time.Duration
}

func (t *RetryingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error
	for i := 0; i <= t.Retries; i++ {
		resp, err = t.Base.RoundTrip(req)
		if err == nil {
			return resp, nil
		}
		if nerr, ok := err.(net.Error); ok && (nerr.Timeout() || nerr.Temporary()) {
			log.WithError(err).Warnf("Retrying proxy request (attempt %d/%d)", i+1, t.Retries)
			time.Sleep(t.Delay)
			continue
		}
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "EOF") {
			log.WithError(err).Warnf("Retrying proxy request (attempt %d/%d)", i+1, t.Retries)
			time.Sleep(t.Delay)
			continue
		}
		break
	}
	return resp, err
}
