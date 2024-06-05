package service

import (
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/zebrunner/esg/utils"
)

func DescribeImages(registryAlly string, repositories []string) ([]*ecr.ImageDetail, error) {
	svc := ecr.New(AwsSess)

	imgDetails := []*ecr.ImageDetail{}
	for _, repository := range repositories {
		describeImagesInput := ecr.DescribeImagesInput{
			RegistryId:     &registryAlly,
			RepositoryName: &repository,
		}

		for {
			describeImagesOutput, err := utils.RetryThrottling(svc.DescribeImages)(&describeImagesInput)
			if err != nil {
				return nil, err
			}

			imgDetails = append(imgDetails, describeImagesOutput.ImageDetails...)

			if describeImagesOutput.NextToken == nil {
				break
			}

			describeImagesInput.NextToken = describeImagesOutput.NextToken
		}
	}

	return imgDetails, nil
}
