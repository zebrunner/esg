package config

import (
	"time"

	"github.com/sirupsen/logrus"
)

var (
	SupportedBrowsers = []string{
		"chrome",
		"firefox",
		"opera",
		"edge",
	}
	VendorPrefix = "zebrunner"
)

var (
	UsePublicIp             bool
	RetryCount              int
	IdleTimeout             time.Duration
	SessionDeleteTimeout    time.Duration
	ServiceStartupTimeout   time.Duration
	VideoRecorderImage      string
	LogLevel                = "debug"
	ReserveInstancesPercent float64

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

	ZebrunnerHost                string
	ZebrunnerIntegrationUser     string
	ZebrunnerIntegrationPassword string
)

func ParseLogLevel() logrus.Level {
	switch LogLevel {
	case "panic":
		return logrus.PanicLevel
	case "fatal":
		return logrus.FatalLevel
	case "error":
		return logrus.ErrorLevel
	case "warning":
		return logrus.WarnLevel
	case "info":
		return logrus.InfoLevel
	case "debug":
		return logrus.DebugLevel
	case "trace":
		return logrus.TraceLevel
	default:
		return logrus.DebugLevel
	}
}

func ZebrunnerIsIntegrated() bool {
	return ZebrunnerHost != "" && ZebrunnerIntegrationUser != "" && ZebrunnerIntegrationPassword != "" && !TrustedMode
}
