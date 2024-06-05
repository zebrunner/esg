package utils

import "sync"

func SendToChanIfNotBlocked[T interface{}](ch chan<- T, entity T) (sent bool) {
	select {
	case ch <- entity:
		sent = true
	default:
		sent = false
	}

	return sent
}

func WaitForAllThreads(wg *sync.WaitGroup, donceCh chan<- interface{}) {
	wg.Wait()
	donceCh <- "done"
}
