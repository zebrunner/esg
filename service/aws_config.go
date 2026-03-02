package service

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	log "github.com/sirupsen/logrus"
	appConfig "github.com/zebrunner/esg/config"
)

var (
	AwsCfg aws.Config

	initialized bool
)

func InitAwsConfig(ctx context.Context) (aws.Config, error) {
	if initialized {
		return AwsCfg, nil
	}

	opts := []func(*config.LoadOptions) error{
		config.WithRegion(appConfig.Conf.AwsRegion),

		config.WithRetryer(func() aws.Retryer {
			return retry.NewStandard(func(o *retry.StandardOptions) {
				o.MaxAttempts = appConfig.Conf.AwsRetry
				o.MaxBackoff = 20 * time.Second
			})
		}),
	}

	if appConfig.Conf.HasStaticCredentials() {
		log.Info("Using static AWS credentials from config")
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				appConfig.Conf.AwsAccessKeyID,
				appConfig.Conf.AwsSecretAccessKey,
				"", // session token (optional)
			),
		))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		log.WithError(err).Error("Failed to load AWS config")
		return aws.Config{}, err
	}

	AwsCfg = cfg
	initialized = true

	log.WithField("region", cfg.Region).Info("AWS SDK v2 config initialized")

	if err := validateAWSConfig(ctx); err != nil {
		return aws.Config{}, err
	}

	return cfg, nil
}

func validateAWSConfig(ctx context.Context) error {
	// Validate credentials
	stsClient := sts.NewFromConfig(AwsCfg)
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		log.WithError(err).Fatal("AWS credentials validation failed")
		return err
	}
	log.WithField("account", aws.ToString(identity.Account)).Info("AWS credentials validated")

	// Validate ECS cluster exists
	if appConfig.Conf.AwsCluster != "" {
		ecsClient := ecs.NewFromConfig(AwsCfg)
		clusters, err := ecsClient.DescribeClusters(ctx, &ecs.DescribeClustersInput{
			Clusters: []string{appConfig.Conf.AwsCluster},
		})
		if err != nil {
			log.WithError(err).Fatal("ECS cluster validation failed")
			return err
		}
		if len(clusters.Clusters) == 0 || clusters.Clusters[0].Status == nil || *clusters.Clusters[0].Status != "ACTIVE" {
			log.WithField("cluster", appConfig.Conf.AwsCluster).Fatal("ECS cluster not found or not active")
			return fmt.Errorf("cluster %s not found or not active", appConfig.Conf.AwsCluster)
		}
		log.WithField("cluster", appConfig.Conf.AwsCluster).Info("ECS cluster validated")
	}

	return nil
}

func GetS3Config(ctx context.Context) (aws.Config, error) {
	conf := &appConfig.Conf

	if conf.S3Region == "" {
		return AwsCfg, nil
	}

	opts := []func(*config.LoadOptions) error{
		config.WithRegion(conf.S3Region),
	}

	if conf.S3AwsAccessKeyID != "" && conf.S3AwsSecretAccessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				conf.S3AwsAccessKeyID,
				conf.S3AwsSecretAccessKey,
				"",
			),
		))
	}

	return config.LoadDefaultConfig(ctx, opts...)
}

func MustInitAwsConfig(ctx context.Context) aws.Config {
	cfg, err := InitAwsConfig(ctx)
	if err != nil {
		log.WithError(err).Fatal("Failed to initialize AWS config")
	}
	return cfg
}

// ResetAwsConfig resets the initialized state.
// This is primarily useful for testing scenarios.
func ResetAwsConfig() {
	initialized = false
	AwsCfg = aws.Config{}
}
