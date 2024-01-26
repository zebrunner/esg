package service

import (
	"fmt"

	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"
)

func DescribeLoadBalancer(loadBalancerName string) (*elbv2.LoadBalancer, error) {
	svc := elbv2.New(AwsSess)

	describeLBInput := elbv2.DescribeLoadBalancersInput{
		Names: []*string{&loadBalancerName},
	}

	describeLBOutput, err := utils.RetryThrottling(svc.DescribeLoadBalancers)(&describeLBInput)
	if err != nil {
		return nil, err
	}

	if len(describeLBOutput.LoadBalancers) < 1 || describeLBOutput.LoadBalancers[0] == nil {
		return nil, fmt.Errorf("load balancer with name %s was not found", config.Conf.AwsAlbName)
	}

	return describeLBOutput.LoadBalancers[0], nil
}

func DescribeTargetGroup(elbArn string) (*elbv2.TargetGroup, error) {
	svc := elbv2.New(AwsSess)

	describeTGInput := elbv2.DescribeTargetGroupsInput{
		LoadBalancerArn: &elbArn,
	}

	describeTGOutput, err := utils.RetryThrottling(svc.DescribeTargetGroups)(&describeTGInput)
	if err != nil {
		return nil, err
	}

	if len(describeTGOutput.TargetGroups) < 1 || describeTGOutput.TargetGroups[0] == nil {
		return nil, fmt.Errorf("target group for elb %s was not found", elbArn)
	}

	return describeTGOutput.TargetGroups[0], nil
}

func RegisterTarget(tagetGroupArn string, port int64) error {
	instanceId, err := utils.GetMetadata(utils.InstanceIdItem)
	if err != nil {
		return err
	}

	svc := elbv2.New(AwsSess)

	registerTargetInput := elbv2.RegisterTargetsInput{
		TargetGroupArn: &tagetGroupArn,
		Targets: []*elbv2.TargetDescription{{
			Id:   &instanceId,
			Port: &port,
		}},
	}

	_, err = utils.RetryThrottling(svc.RegisterTargets)(&registerTargetInput)

	return err
}

func DeregisterTarget(tagetGroupArn string, port int64) error {
	instanceId, err := utils.GetMetadata(utils.InstanceIdItem)
	if err != nil {
		return err
	}

	svc := elbv2.New(AwsSess)
	deregisterTargetInput := elbv2.DeregisterTargetsInput{
		TargetGroupArn: &tagetGroupArn,
		Targets: []*elbv2.TargetDescription{{
			Id:   &instanceId,
			Port: &port,
		}},
	}

	_, err = utils.RetryThrottling(svc.DeregisterTargets)(&deregisterTargetInput)

	return err
}
