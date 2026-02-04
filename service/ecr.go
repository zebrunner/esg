package service

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrTypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

func DescribeImages(ctx context.Context, registryId string, repositories []string) ([]ecrTypes.ImageDetail, error) {
	svc := ecr.NewFromConfig(AwsCfg)

	imgDetails := []ecrTypes.ImageDetail{}
	for _, repository := range repositories {
		paginator := ecr.NewDescribeImagesPaginator(svc, &ecr.DescribeImagesInput{
			RegistryId:     aws.String(registryId),
			RepositoryName: aws.String(repository),
		})

		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			imgDetails = append(imgDetails, page.ImageDetails...)
		}
	}

	return imgDetails, nil
}
