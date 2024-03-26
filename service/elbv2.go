package service

import (
	"fmt"

	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/zebrunner/esg/utils"
)

func DescribeLoadBalancer(lbArn string) (*elbv2.LoadBalancer, error) {
	svc := elbv2.New(AwsSess)

	describeLBInput := elbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: []*string{&lbArn},
	}

	describeLBOutput, err := utils.RetryThrottling(svc.DescribeLoadBalancers)(&describeLBInput)
	if err != nil {
		return nil, err
	}

	if len(describeLBOutput.LoadBalancers) < 1 || describeLBOutput.LoadBalancers[0] == nil {
		return nil, fmt.Errorf("load balancer %s was not found", lbArn)
	}

	return describeLBOutput.LoadBalancers[0], nil
}

func DescribeTargetGroup(tgName string) (*elbv2.TargetGroup, error) {
	svc := elbv2.New(AwsSess)

	describeTGInput := elbv2.DescribeTargetGroupsInput{
		Names: []*string{&tgName},
	}

	describeTGOutput, err := utils.RetryThrottling(svc.DescribeTargetGroups)(&describeTGInput)
	if err != nil {
		return nil, err
	}

	if len(describeTGOutput.TargetGroups) < 1 || describeTGOutput.TargetGroups[0] == nil {
		return nil, fmt.Errorf("target group %s was not found", tgName)
	}

	return describeTGOutput.TargetGroups[0], nil
}

func RegisterTarget(targetGroup *elbv2.TargetGroup, port int64) error {
	id, err := getTargetId(targetGroup)
	if err != nil {
		return err
	}

	registerTargetInput := elbv2.RegisterTargetsInput{
		TargetGroupArn: targetGroup.TargetGroupArn,
		Targets: []*elbv2.TargetDescription{{
			Id:   &id,
			Port: &port,
		}},
	}

	svc := elbv2.New(AwsSess)
	_, err = utils.RetryThrottling(svc.RegisterTargets)(&registerTargetInput)

	return err
}

func DeregisterTarget(targetGroup *elbv2.TargetGroup, port int64) error {
	id, err := getTargetId(targetGroup)
	if err != nil {
		return err
	}

	deregisterTargetInput := elbv2.DeregisterTargetsInput{
		TargetGroupArn: targetGroup.TargetGroupArn,
		Targets: []*elbv2.TargetDescription{{
			Id:   &id,
			Port: &port,
		}},
	}

	svc := elbv2.New(AwsSess)
	_, err = utils.RetryThrottling(svc.DeregisterTargets)(&deregisterTargetInput)

	return err
}

func getTargetId(targetGroup *elbv2.TargetGroup) (string, error) {
	switch *targetGroup.TargetType {
	case "ip":
		if targetGroup.IpAddressType != nil && *targetGroup.IpAddressType == "ipv6" {
			return utils.GetMetadata(utils.Ipv6Item)
		} else {
			return utils.GetMetadata(utils.PrivateIpv4Item)
		}
	case "instance":
		return utils.GetMetadata(utils.InstanceIdItem)
	default:
		return "", fmt.Errorf("unsupported target group type: %s", *targetGroup.TargetType)
	}
}
