package utils

import (
	"net/http"
	"time"
)

// RetryThrottling removed - AWS SDK v2 has built-in retry mechanism
// configured in service/aws_config.go via retry.NewStandard()

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
