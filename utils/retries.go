package utils

import (
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

func RetryThrottling[T, R interface{}](executeFunc func(T) (R, error)) func(T) (R, error) {
	return func(arg T) (R, error) {
		var result R
		var err error
		funcName := runtime.FuncForPC(reflect.ValueOf(executeFunc).Pointer()).Name()
		l := log.WithField("func", funcName)

		retryCount := 10
		var i int
		for i = 0; i < retryCount; i++ {
			result, err = executeFunc(arg)
			if err != nil {
				if strings.Contains(err.Error(), "ThrottlingException") {
					l.WithError(err).WithField("retry", i).Debug()
					// starting from 1 sec to 10 secs
					time.Sleep(time.Duration(i+1) * time.Second)
				} else if strings.Contains(err.Error(), "ClusterNotFoundException") || strings.Contains(err.Error(), "NoCredentialProviders") {
					ExitWithError(err, "AWS returned fatal error on api call", l)
				} else {
					break
				}
			} else {
				return result, err
			}
		}

		if i != 0 {
			l.WithError(err).Debugf("RetryThrottling: performed %d retries", i)
		}
		return result, err
	}
}

type Sender interface {
	Do(*http.Request) (*http.Response, error)
}

func RetryOnSendFailure(sendFn Sender, retryCount int, retryDelay time.Duration) func(*http.Request) (*http.Response, error) {
	return func(req *http.Request) (*http.Response, error) {
		var err error
		var res *http.Response
		for i := 0; i < retryCount; i++ {
			res, err = sendFn.Do(req)
			if err == nil {
				break
			}

			time.Sleep(retryDelay)
		}

		return res, err
	}
}
