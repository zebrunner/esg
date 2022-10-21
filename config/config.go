package config

import (
	"flag"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	SupportedRepositories = []string{
		"chrome",
		"firefox",
		"edge",
		"redroid",
	}
	VendorPrefix = "zebrunner"
	Conf         = Config{}
)

func init() {

}

type Config struct {
	// AWS settings
	AwsRegion           string
	AwsRetry            int
	AwsCluster          string
	AwsAutoScalingGroup string
	AwsAccessKeyID      string
	AwsSecretAccessKey  string

	// Session resource limitations
	MinMemory            int64
	MinMemoryReservation int64
	MaxMemory            int64
	MaxMemoryReservation int64
	MinCpu               int64
	MaxCpu               int64

	// Timeouts
	IdleTimeout             time.Duration
	SessionStartupTimeout   time.Duration
	SessionDeleteTimeout    time.Duration
	ServiceStartupTimeout   time.Duration
	InstanceCooldownTimeout time.Duration

	// External connections
	DbConnectionString           string
	RedisConnectionString        string
	ZebrunnerHost                string
	ZebrunnerIntegrationUser     string
	ZebrunnerIntegrationPassword string

	UsePublicIp             bool
	S3Bucket                string // For static artifacts
	TrustedMode             bool
	Tenant                  string
	LogLevel                string
	ReserveInstancesPercent float64

	BrowsersFile string
}

func init() {
	flag.StringVar(&Conf.AwsRegion, "aws-region", "us-east-1", "AWS region name")
	flag.IntVar(&Conf.AwsRetry, "aws-retry", 10, "AWS client retry count")
	flag.StringVar(&Conf.AwsCluster, "aws-cluster", "esg", "AWS ECS cluster name")
	flag.StringVar(&Conf.AwsAutoScalingGroup, "aws-auto-scaling-group", "esg-asg", "AWS auto scaling group name")
	flag.StringVar(&Conf.AwsAccessKeyID, "aws-access-key-id", "", "Access key for S3 bucket")
	flag.StringVar(&Conf.AwsSecretAccessKey, "aws-secret-access-key", "", "Secret key for S3 bucket")

	flag.Int64Var(&Conf.MinMemory, "min-memory", 1536, "minimum memory limitation for session")
	flag.Int64Var(&Conf.MinMemoryReservation, "min-memory-reservation", 1536, "minimum memory reservation limitation for session")
	flag.Int64Var(&Conf.MaxMemory, "max-memory", 8192, "maximum memory limitation for session")
	flag.Int64Var(&Conf.MaxMemoryReservation, "max-memory-reservation", 8192, "maximum memory reservation limitation for session")
	flag.Int64Var(&Conf.MinCpu, "min-cpu", 1024, "minimum CPU limitation for session")
	flag.Int64Var(&Conf.MaxCpu, "max-cpu", 7936, "maximum CPU limitation for session")

	flag.DurationVar(&Conf.IdleTimeout, "idle-timeout", 60*time.Second, "Session idle timeout in time.Duration format")
	flag.DurationVar(&Conf.SessionStartupTimeout, "session-startup-timeout", 90*time.Second, "Session healthcheck(s) startup timeout in time.Duration format")
	flag.DurationVar(&Conf.SessionDeleteTimeout, "session-delete-timeout", 30*time.Second, "Session delete timeout in time.Duration format")
	flag.DurationVar(&Conf.ServiceStartupTimeout, "service-startup-timeout", 10*time.Minute, "Service startup timeout in time.Duration format")
	flag.DurationVar(&Conf.InstanceCooldownTimeout, "instance-cooldown-timeout", 4*time.Minute, "Time after instance start when shutdown is prohibited on scale down in time.Duration format")

	flag.StringVar(&Conf.DbConnectionString, "db-connection", "", "Connection string for database")
	flag.StringVar(&Conf.RedisConnectionString, "aws-elastic-cache", "localhost:6379", "Connection string for Session cache")
	flag.StringVar(&Conf.ZebrunnerHost, "zebrunner-host", "", "Host for zebrunner integration for this environment")
	flag.StringVar(&Conf.ZebrunnerIntegrationUser, "zebrunner-integration-user", "", "User for zebrunner for current env")
	flag.StringVar(&Conf.ZebrunnerIntegrationPassword, "zebrunner-integration-password", "", "Password for zebrunner for current env")

	flag.BoolVar(&Conf.UsePublicIp, "use-public-ip", false, "Use or no public ip address for browser slave instances")
	flag.StringVar(&Conf.S3Bucket, "s3-bucket", "", "S3 Bucket name for pushing artifacts")
	flag.StringVar(&Conf.Tenant, "tenant", "", "Zebrunner tenant name")
	flag.BoolVar(&Conf.TrustedMode, "trusted", false, "If trusted mode enabled hub does not require any auth")
	flag.StringVar(&Conf.LogLevel, "log-level", "debug", "Desired log level. Valid levels: `panic`, `fatal`, `error`, `warning`, `info`, `debug`, `trace`")
	flag.Float64Var(&Conf.ReserveInstancesPercent, "reserve-instances-percent", 0.25, "Reserved cluster capacity quota during scale up and down operations")

	flag.StringVar(&Conf.BrowsersFile, "browsers-file", "", "Path to txt file with supported browsers")
}

func (c *Config) ParseLogLevel() logrus.Level {
	switch c.LogLevel {
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

func (c *Config) ZebrunnerIsIntegrated() bool {
	return c.ZebrunnerHost != "" && c.ZebrunnerIntegrationUser != "" && c.ZebrunnerIntegrationPassword != "" && !c.TrustedMode
}
