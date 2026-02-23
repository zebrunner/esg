package service

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2Types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"
)

func DescribeLoadBalancer(ctx context.Context, lbArn string) (*elbv2Types.LoadBalancer, error) {
	svc := elbv2.NewFromConfig(AwsCfg)

	describeLBInput := &elbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{lbArn},
	}

	describeLBOutput, err := svc.DescribeLoadBalancers(ctx, describeLBInput)
	if err != nil {
		return nil, err
	}

	if len(describeLBOutput.LoadBalancers) < 1 {
		return nil, fmt.Errorf("load balancer %s was not found", lbArn)
	}

	return &describeLBOutput.LoadBalancers[0], nil
}

func DescribeTargetGroup(ctx context.Context, tgName string) (*elbv2Types.TargetGroup, error) {
	svc := elbv2.NewFromConfig(AwsCfg)

	describeTGInput := &elbv2.DescribeTargetGroupsInput{
		Names: []string{tgName},
	}

	describeTGOutput, err := svc.DescribeTargetGroups(ctx, describeTGInput)
	if err != nil {
		return nil, err
	}

	if len(describeTGOutput.TargetGroups) < 1 {
		return nil, fmt.Errorf("target group %s was not found", tgName)
	}

	return &describeTGOutput.TargetGroups[0], nil
}

func DescribeListener(ctx context.Context, lbArn string) (*elbv2Types.Listener, error) {
	svc := elbv2.NewFromConfig(AwsCfg)

	describeListenerInput := &elbv2.DescribeListenersInput{
		LoadBalancerArn: aws.String(lbArn),
	}

	describeListenerOutput, err := svc.DescribeListeners(ctx, describeListenerInput)
	if err != nil {
		return nil, err
	}

	if len(describeListenerOutput.Listeners) < 1 {
		return nil, fmt.Errorf("no listener is attached to load balancer: %s", lbArn)
	}

	return &describeListenerOutput.Listeners[0], nil
}

func RegisterTarget(ctx context.Context, targetGroup *elbv2Types.TargetGroup, port int64) error {
	id, err := getTargetId(targetGroup)
	if err != nil {
		return err
	}

	portInt32 := int32(port)
	registerTargetInput := &elbv2.RegisterTargetsInput{
		TargetGroupArn: targetGroup.TargetGroupArn,
		Targets: []elbv2Types.TargetDescription{{
			Id:   aws.String(id),
			Port: &portInt32,
		}},
	}

	svc := elbv2.NewFromConfig(AwsCfg)
	_, err = svc.RegisterTargets(ctx, registerTargetInput)

	return err
}

func DeregisterTarget(ctx context.Context, targetGroup *elbv2Types.TargetGroup, port int64) error {
	id, err := getTargetId(targetGroup)
	if err != nil {
		return err
	}

	portInt32 := int32(port)
	deregisterTargetInput := &elbv2.DeregisterTargetsInput{
		TargetGroupArn: targetGroup.TargetGroupArn,
		Targets: []elbv2Types.TargetDescription{{
			Id:   aws.String(id),
			Port: &portInt32,
		}},
	}

	svc := elbv2.NewFromConfig(AwsCfg)
	_, err = svc.DeregisterTargets(ctx, deregisterTargetInput)

	return err
}

func getTargetId(targetGroup *elbv2Types.TargetGroup) (string, error) {
	// Use explicit target ID from config when IMDS is disabled (hop limit = 1)
	if config.Conf.AwsTargetId != "" {
		return config.Conf.AwsTargetId, nil
	}

	switch targetGroup.TargetType {
	case elbv2Types.TargetTypeEnumIp:
		if targetGroup.IpAddressType == elbv2Types.TargetGroupIpAddressTypeEnumIpv6 {
			return utils.GetMetadata(utils.Ipv6Item)
		}
		return utils.GetMetadata(utils.PrivateIpv4Item)
	case elbv2Types.TargetTypeEnumInstance:
		return utils.GetMetadata(utils.InstanceIdItem)
	default:
		return "", fmt.Errorf("unsupported target group type: %s", targetGroup.TargetType)
	}
}
