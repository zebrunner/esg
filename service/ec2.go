package service

import (
	"github.com/aws/aws-sdk-go/service/ec2"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/utils"
	"math"
)

func DescribeInstances(ec2InstanceIdPtrs []*string, ec2Svc *ec2.EC2) ([]*ec2.Instance, error) {
	var ec2Instances []*ec2.Instance
	for {
		input := ec2.DescribeInstancesInput{
			InstanceIds: ec2InstanceIdPtrs,
		}

		ec2Result, err := utils.RetryThrottling(ec2Svc.DescribeInstances)(&input)
		if err != nil {
			log.WithField("error", err).Error("Failed to DescribeInstances!")
			return nil, err
		}

		for _, reservation := range ec2Result.Reservations {
			ec2Instances = append(ec2Instances, reservation.Instances...)
		}

		if ec2Result.NextToken != nil {
			input.NextToken = ec2Result.NextToken
		} else {
			break
		}
	}

	return ec2Instances, nil
}

func DescribeInstancesStatus(ec2InstanceIdPtrs []*string, ec2Svc *ec2.EC2) ([]*string, []*string, error) {
	input := &ec2.DescribeInstanceStatusInput{
		InstanceIds: ec2InstanceIdPtrs,
	}

	healthyInstanceIdPtrs := make([]*string, 0)
	unhealthyInstanceIdPtrs := make([]*string, 0)

	for {
		statusOutput, err := utils.RetryThrottling(ec2Svc.DescribeInstanceStatus)(input)
		if err != nil {
			log.WithField("error", err).Error("Failed to DescribeInstancesStatus!")
			return nil, nil, err
		}

		instanceStatuses := statusOutput.InstanceStatuses
		for _, is := range instanceStatuses {
			l := log.WithField("_ec2Id", *is.InstanceId)

			if *is.InstanceStatus.Status == ec2.SummaryStatusImpaired || *is.SystemStatus.Status == ec2.SummaryStatusImpaired {
				l.Info("Unhealthy instance")
				unhealthyInstanceIdPtrs = append(unhealthyInstanceIdPtrs, is.InstanceId)
			} else {
				l.Trace("Healthy instance")
				healthyInstanceIdPtrs = append(healthyInstanceIdPtrs, is.InstanceId)
			}
		}

		if statusOutput.NextToken != nil {
			input.NextToken = statusOutput.NextToken
		} else {
			break
		}

	}

	return healthyInstanceIdPtrs, unhealthyInstanceIdPtrs, nil
}

// TerminateInstances need's permissons for performing ec2Svc.TerminateInstances call
func TerminateInstances(ec2InstanceIdPtrs []*string, ec2Svc *ec2.EC2) error {
	// ec2 constraints: Up to 1000 instance IDs. We recommend breaking up this request into smaller batches
	// paginating only up to 100 instance IDs
	pages := paginate(ec2InstanceIdPtrs, 100)
	for _, page := range pages {
		input := &ec2.TerminateInstancesInput{
			InstanceIds: page,
		}

		_, err := utils.RetryThrottling(ec2Svc.TerminateInstances)(input)
		if err != nil {
			return err
		}
	}

	return nil
}

func paginate[T interface{}](l []T, size int) [][]T {
	numPages := int(math.Ceil(float64(len(l)) / float64(size)))
	pages := make([][]T, numPages)
	for i := 0; i < numPages; i++ {
		left := i * size
		right := (i + 1) * size
		if right > len(l) {
			right = len(l)
		}
		pages[i] = l[left:right]
	}

	return pages
}
