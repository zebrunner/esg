package utils

import "sync"

func WaitForAllThreads(wg *sync.WaitGroup, donceCh chan<- interface{}) {
	wg.Wait()
	donceCh <- "done"
}
