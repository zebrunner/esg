package config

import (
	"flag"
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// public zebrunner ECR docker registry
	ZebrunnerEcrRegistryUri = "public.ecr.aws/zebrunner"
)

var (
	VendorPrefix = "zebrunner"
	Conf         = Config{}
	RouterUUID   = "_uuid"
	TaskIdKey    = "_taskId"
	SessionIdKey = "sessionId"
	Version      = os.Getenv("VERSION")
)

type Config struct {
	// AWS settings
	AwsRegion                string
	AwsRetry                 int
	AwsCluster               string
	AwsLinuxCapacityProvider string
	AwsWinCapacityProvider   string
	AwsAccessKeyID           string
	AwsSecretAccessKey       string
	AwsTaskRoleArn           string
	AwsTargetGroup           string
	AwsEsgUrl                string

	// Timeouts
	MaxIdleTimeout               time.Duration
	IdleTimeout                  time.Duration
	SessionDeleteTimeout         time.Duration
	ServiceStartupTimeout        time.Duration
	InstanceCooldownTimeout      time.Duration
	ContainerInstanceInitTimeout time.Duration
	MaxTimeout                   time.Duration

	// External connections
	DbConnectionString          string
	RedisConnectionString       string
	DefinitionsConnectionString string

	ZebrunnerHost                string
	ZebrunnerIntegrationUser     string
	ZebrunnerIntegrationPassword string

	UsePublicIp          bool
	S3Bucket             string // For static artifacts
	S3Region             string
	S3AwsAccessKeyID     string
	S3AwsSecretAccessKey string

	LogLevel       string
	RecorderLogLvl string
	AwsLogsEnabled bool

	ReserveInstancesPercent float64
	ReserveMaxCapacity      int64

	ImageRepositories string
	ExcludeBrowsers   string

	ProductionEnv bool
	SingleTenant  bool
	ExternalPort  int64

	OldAbortApi bool
}

func init() {
	flag.StringVar(&Conf.AwsRegion, "aws-region", "us-east-1", "AWS region name")
	flag.IntVar(&Conf.AwsRetry, "aws-retry", 10, "AWS client retry count")
	flag.StringVar(&Conf.AwsCluster, "aws-cluster", "esg", "AWS ECS cluster name")
	flag.StringVar(&Conf.AwsLinuxCapacityProvider, "aws-linux-capacity-provider", "esg-linux-capacityprovider", "AWS capacity provider for linux instances")
	flag.StringVar(&Conf.AwsWinCapacityProvider, "aws-win-capacity-provider", "esg-win-capacityprovider", "AWS capacity provicer for windows instances")
	flag.StringVar(&Conf.AwsAccessKeyID, "aws-access-key-id", "", "Access key for AWS services")
	flag.StringVar(&Conf.AwsSecretAccessKey, "aws-secret-access-key", "", "Secret key for AWS services")
	flag.StringVar(&Conf.AwsTaskRoleArn, "aws-task-role-arn", "", "Role that would be assigned to all task's definitions")
	flag.StringVar(&Conf.AwsTargetGroup, "aws-target-group", "", "Application load balancer name")

	flag.DurationVar(&Conf.MaxIdleTimeout, "max-idle-timeout", 20*time.Minute, "Maximum session idle timeout time that could be set by user's capabilities")
	flag.DurationVar(&Conf.IdleTimeout, "idle-timeout", 60*time.Second, "Session idle timeout in time.Duration format")
	flag.DurationVar(&Conf.SessionDeleteTimeout, "session-delete-timeout", 30*time.Second, "Session delete timeout in time.Duration format")
	flag.DurationVar(&Conf.ServiceStartupTimeout, "service-startup-timeout", 10*time.Minute, "Service startup timeout in time.Duration format")
	flag.DurationVar(&Conf.InstanceCooldownTimeout, "instance-cooldown-timeout", 4*time.Minute, "Time after instance start when shutdown is prohibited on scale down in time.Duration format")
	flag.DurationVar(&Conf.ContainerInstanceInitTimeout, "container-instance-init-timeout", 10*time.Minute, "Time for ec2 instance after launch to initialize container-instance for asg in time.Duration format")
	flag.DurationVar(&Conf.MaxTimeout, "max-timeout", 24*time.Hour, "Maximum valid task/session timeout in time.Duration format")

	flag.StringVar(&Conf.DbConnectionString, "db-connection", "localhost:5432", "Connection string for database")
	flag.StringVar(&Conf.RedisConnectionString, "aws-elastic-cache", "localhost:6379", "Connection string for Session cache")
	flag.StringVar(&Conf.DefinitionsConnectionString, "definitions-connection", "localhost:5555", "Connection string for task-definitions service")

	flag.StringVar(&Conf.ZebrunnerHost, "zebrunner-host", "", "Host for zebrunner integration for this environment")
	flag.StringVar(&Conf.ZebrunnerIntegrationUser, "zebrunner-integration-user", "", "User for zebrunner for current env")
	flag.StringVar(&Conf.ZebrunnerIntegrationPassword, "zebrunner-integration-password", "", "Password for zebrunner for current env")

	flag.BoolVar(&Conf.UsePublicIp, "use-public-ip", false, "Use or no public ip address for browser slave instances")
	flag.StringVar(&Conf.S3Bucket, "s3-bucket", "", "S3 Bucket name for pushing artifacts")
	flag.StringVar(&Conf.S3Region, "s3-region", "", "S3 Bucket region for pushing artifacts")
	flag.StringVar(&Conf.S3AwsAccessKeyID, "s3-aws-access-key-id", "", "Access key for S3 bucket")
	flag.StringVar(&Conf.S3AwsSecretAccessKey, "s3-aws-secret-access-key", "", "Secret key for S3 bucket")

	flag.StringVar(&Conf.LogLevel, "log-level", "debug", "Desired log level. Valid levels: `panic`, `fatal`, `error`, `warning`, `info`, `debug`, `trace`")
	flag.BoolVar(&Conf.AwsLogsEnabled, "aws-logs-enabled", false, "Aws cloud watch logs for ecs tasks")

	flag.Float64Var(&Conf.ReserveInstancesPercent, "reserve-instances-percent", 0.25, "Reserved cluster capacity quota during scale up and down operations")
	flag.Int64Var(&Conf.ReserveMaxCapacity, "reserve-max-capacity", 5, "Reservation instance limit")

	flag.StringVar(&Conf.ImageRepositories, "image-repositories", "Zebrunner:chrome", "Pattern of supported browser images")
	flag.StringVar(&Conf.ExcludeBrowsers, "exclude-browsers", "", "Pattern for excluding browsers from available images")

	flag.BoolVar(&Conf.SingleTenant, "single-tenant", false, "Single tenant mode")
	flag.BoolVar(&Conf.ProductionEnv, "production-env", true, "Service configuration mode")
	flag.Int64Var(&Conf.ExternalPort, "external-port", 0, "Router's external listening port")

	flag.BoolVar(&Conf.OldAbortApi, "old-abort-api", false, "Usage of reporting's old api abort path")
}

func (c *Config) ParseLogLevel() logrus.Level {
	c.RecorderLogLvl = "debug"
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
		c.RecorderLogLvl = "info"
		return logrus.InfoLevel
	case "debug":
		return logrus.DebugLevel
	case "trace":
		return logrus.TraceLevel
	default:
		return logrus.DebugLevel
	}
}
