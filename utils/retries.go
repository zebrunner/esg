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
		for i := 1; i <= retryCount; i++ {
			result, err = executeFunc(arg)
			if err != nil {
				if strings.Contains(err.Error(), "ThrottlingException") {
					l.WithError(err).WithField("retry", i)
					time.Sleep(time.Duration(i) * time.Second)
				} else if strings.Contains(err.Error(), "ClusterNotFoundException") {
					l.WithError(err).Error("Stopping container because of exception")
					os.Exit(1)
				} else {
					break
				}

			} else {
				return result, err
			}
		}

		l.WithError(err).Errorf("Couldn't perform func in %d retries", retryCount)
		return result, err
	}
}
