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

	go pp.decreasePause(time.Duration(pause+pp.pauseIncrement) * time.Millisecond)

	return time.Duration(pause) * time.Millisecond
}

func (pp *ProgressivePause) decreasePause(delayBerforeDeacrease time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), delayBerforeDeacrease)
	defer cancel()
	<-ctx.Done()
	atomic.AddInt64(&pp.pause, -pp.pauseIncrement)
}
