package config

import (
	"time"
)

var (
	EnableFileUpload      bool
	RetryCount            int
	Timeout               time.Duration
	MaxTimeout            time.Duration
	SessionDeleteTimeout  time.Duration
	ServiceStartupTimeout time.Duration
	VideoRecorderImage    string

	AwsRegion            string
	AwsRetry             int
	AwsCluster           string
	AwsElasticCache      string
	AwsAutoScalingGroup  string
	MinMemory            int
	MinMemoryReservation int
	MaxMemory            int
	MaxMemoryReservation int
	MinCpu               int
	MaxCpu               int
	S3Bucket             string
	AwsAccessKeyID       string
	AwsSecretAccessKey   string
	DbConnectionString   string
	TrustedMode          bool
	Tenant               string
)
