package utils

import "time"

func CreateTimer(duration time.Duration) func() <-chan interface{} {
	firstAttempt := true
	ch := make(chan interface{})
	return func() <-chan interface{} {
		go func() {
			if firstAttempt {
				firstAttempt = false
			} else {
				time.Sleep(duration)
			}
			ch <- "brother ouu"
		}()
		return ch
	}
}
