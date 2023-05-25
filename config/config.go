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
		"cypress-chrome",
		"cypress-chromium",
		"cypress-edge",
		"cypress-firefox",
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
	MaxMemory            int64
	MaxCpu               int64

	// Timeouts
	IdleTimeout             time.Duration
	SessionStartupTimeout   time.Duration
	SessionDeleteTimeout    time.Duration
	ServiceStartupTimeout   time.Duration
	InstanceCooldownTimeout time.Duration
	MaxTimeout              time.Duration

	// External connections
	DbConnectionString           string
	RedisConnectionString        string
	ZebrunnerHost                string
	ZebrunnerIntegrationUser     string
	ZebrunnerIntegrationPassword string

	UsePublicIp             bool
	S3Bucket                string // For static artifacts
	S3Region		string
	S3AwsAccessKeyID	string
	S3AwsSecretAccessKey	string

	LogLevel                string
	ReserveInstancesPercent float64

	ExcludeBrowsers string
}

func init() {
	flag.StringVar(&Conf.AwsRegion, "aws-region", "us-east-1", "AWS region name")
	flag.IntVar(&Conf.AwsRetry, "aws-retry", 10, "AWS client retry count")
	flag.StringVar(&Conf.AwsCluster, "aws-cluster", "esg", "AWS ECS cluster name")
	flag.StringVar(&Conf.AwsAutoScalingGroup, "aws-auto-scaling-group", "esg-asg", "AWS auto scaling group name")
	flag.StringVar(&Conf.AwsAccessKeyID, "aws-access-key-id", "", "Access key for AWS services")
	flag.StringVar(&Conf.AwsSecretAccessKey, "aws-secret-access-key", "", "Secret key for AWS services")

	flag.Int64Var(&Conf.MaxMemory, "max-memory", 28675, "maximum memory limitation for session") // max memory for c5a.4xlarge
	flag.Int64Var(&Conf.MaxCpu, "max-cpu", 16384, "maximum CPU limitation for session") //max cpu for c5a.4xlarge

	flag.DurationVar(&Conf.IdleTimeout, "idle-timeout", 60*time.Second, "Session idle timeout in time.Duration format")
	flag.DurationVar(&Conf.SessionStartupTimeout, "session-startup-timeout", 180*time.Second, "Session startup timeout in time.Duration format")
	flag.DurationVar(&Conf.SessionDeleteTimeout, "session-delete-timeout", 30*time.Second, "Session delete timeout in time.Duration format")
	flag.DurationVar(&Conf.ServiceStartupTimeout, "service-startup-timeout", 10*time.Minute, "Service startup timeout in time.Duration format")
	flag.DurationVar(&Conf.InstanceCooldownTimeout, "instance-cooldown-timeout", 4*time.Minute, "Time after instance start when shutdown is prohibited on scale down in time.Duration format")
        flag.DurationVar(&Conf.MaxTimeout, "max-timeout", 24*time.Hour, "Maximum valid task/session timeout in time.Duration format")

	flag.StringVar(&Conf.DbConnectionString, "db-connection", "", "Connection string for database")
	flag.StringVar(&Conf.RedisConnectionString, "aws-elastic-cache", "localhost:6379", "Connection string for Session cache")
	flag.StringVar(&Conf.ZebrunnerHost, "zebrunner-host", "", "Host for zebrunner integration for this environment")
	flag.StringVar(&Conf.ZebrunnerIntegrationUser, "zebrunner-integration-user", "", "User for zebrunner for current env")
	flag.StringVar(&Conf.ZebrunnerIntegrationPassword, "zebrunner-integration-password", "", "Password for zebrunner for current env")

	flag.BoolVar(&Conf.UsePublicIp, "use-public-ip", false, "Use or no public ip address for browser slave instances")
	flag.StringVar(&Conf.S3Bucket, "s3-bucket", "", "S3 Bucket name for pushing artifacts")
	flag.StringVar(&Conf.S3Region, "s3-region", "", "S3 Bucket region for pushing artifacts")
	flag.StringVar(&Conf.S3AwsAccessKeyID, "s3-aws-access-key-id", "", "Access key for S3 bucket")
	flag.StringVar(&Conf.S3AwsSecretAccessKey, "s3-aws-secret-access-key", "", "Secret key for S3 bucket")

	flag.StringVar(&Conf.LogLevel, "log-level", "debug", "Desired log level. Valid levels: `panic`, `fatal`, `error`, `warning`, `info`, `debug`, `trace`")
	flag.Float64Var(&Conf.ReserveInstancesPercent, "reserve-instances-percent", 0.25, "Reserved cluster capacity quota during scale up and down operations")

	flag.StringVar(&Conf.ExcludeBrowsers, "exclude-browsers", "", "Pattern for excluding browsers from available images")
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
	return c.ZebrunnerHost != "" && c.ZebrunnerIntegrationUser != "" && c.ZebrunnerIntegrationPassword != ""
}
