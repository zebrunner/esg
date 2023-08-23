package utils

import (
	"os"
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
					l.WithError(err).Error("Stopping container because of exception")
					os.Exit(1)
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
