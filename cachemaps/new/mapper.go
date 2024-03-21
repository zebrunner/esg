package new

import (
	"time"

	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/environment"
)

type TaskStatus int

const (
	SessionActive TaskStatus = iota
	SessionStopped
)

type StoppedReason string

const (
	TaskStartupFailure     StoppedReason = "task startup failure"
	SessiongStartupFailure StoppedReason = "healthy task failed to start session"
	TaskUnhealthy          StoppedReason = "task aborted due to unhealthy status"
	TaskMaxTimeout         StoppedReason = "task aborted due to the max timeout"
	TaskAborted            StoppedReason = "task aborted"
	TaskFinished           StoppedReason = "task finished"
	TaskLost               StoppedReason = "task aborted as it wasn't found in cache"
)

type IdMapper struct {
	RouterUUID       string
	TaskId           string                     `json:",omitempty"`
	SessionID        string                     `json:",omitempty"`
	Capabilities     *capabilities.Capabilities `json:",omitempty"`
	Status           TaskStatus
	UsageTracked     bool
	HealthAt         *time.Time
	CurrentSessionID string        `json:",omitempty"`
	StopReason       StoppedReason `json:",omitempty"`
	AccessedAt       time.Time     `json:",omitempty"`
	IdleTimeout      float64
	Network          environment.NetworkConfiguration
	Workspace        string
}
