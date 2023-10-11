package utils

import (
	"context"
	"sync/atomic"
	"time"
)

type ProgressivePause struct {
	pause          int64
	pauseIncrement int64
}

func CreateProgressivePause(startTimeMs int64, stepTimeMs int64) ProgressivePause {
	return ProgressivePause{
		pause:          startTimeMs,
		pauseIncrement: stepTimeMs,
	}
}

func (pp *ProgressivePause) GetPause() time.Duration {
	pause := pp.pause
	atomic.AddInt64(&pp.pause, pp.pauseIncrement)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(pause+pp.pauseIncrement)*time.Millisecond)
		defer cancel()
		<-ctx.Done()
		atomic.AddInt64(&pp.pause, pp.pauseIncrement)
	}()

	return time.Duration(pause) * time.Millisecond
}
