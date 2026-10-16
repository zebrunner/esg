package monitoring

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/service"
)

func taskStopped(ctx context.Context, taskID string) bool {
	result, err := service.DescribeTask(ctx, taskID)
	return err == nil && len(result.Tasks) == 1 && len(result.Failures) == 0 && aws.ToString(result.Tasks[0].LastStatus) == "STOPPED"
}

func recorderURL(ctx context.Context, entity *mapper.Mapper, endpointName string) (*url.URL, bool, error) {
	endpoint, ok := entity.Network.Endpoints[endpointName]
	if !ok {
		return nil, false, fmt.Errorf("missing recorder endpoint")
	}
	if entity.Network.IP != "" && endpoint.HostPort > 0 {
		target := &url.URL{Scheme: "http", Host: net.JoinHostPort(entity.Network.IP, strconv.FormatInt(endpoint.HostPort, 10)), Path: endpoint.Path}
		return target, false, nil
	}

	result, err := service.DescribeTask(ctx, entity.TaskId)
	if err != nil {
		return nil, false, err
	}
	if len(result.Tasks) != 1 || len(result.Failures) != 0 {
		return nil, false, fmt.Errorf("task not found")
	}
	task := result.Tasks[0]
	if aws.ToString(task.LastStatus) == "STOPPED" {
		return nil, true, nil
	}
	var port int32
	for _, container := range task.Containers {
		if aws.ToString(container.Name) == "recorder" {
			for _, binding := range container.NetworkBindings {
				if aws.ToInt32(binding.ContainerPort) == 9080 {
					port = aws.ToInt32(binding.HostPort)
				}
			}
		}
	}
	if port <= 0 || aws.ToString(task.ContainerInstanceArn) == "" {
		return nil, false, fmt.Errorf("recorder port is not assigned")
	}
	instances, err := service.DescribeContainerInstances(ctx, []string{aws.ToString(task.ContainerInstanceArn)}, ecs.NewFromConfig(service.AwsCfg))
	if err != nil {
		return nil, false, err
	}
	if len(instances) != 1 || aws.ToString(instances[0].Ec2InstanceId) == "" {
		return nil, false, fmt.Errorf("container instance not found")
	}
	hosts, err := service.DescribeInstances(ctx, []string{aws.ToString(instances[0].Ec2InstanceId)}, ec2.NewFromConfig(service.AwsCfg))
	if err != nil {
		return nil, false, err
	}
	if len(hosts) != 1 {
		return nil, false, fmt.Errorf("recorder host not found")
	}
	ip := aws.ToString(hosts[0].PrivateIpAddress)
	if config.Conf.UsePublicIp {
		ip = aws.ToString(hosts[0].PublicIpAddress)
	}
	if ip == "" {
		return nil, false, fmt.Errorf("recorder host has no IP")
	}
	return &url.URL{Scheme: "http", Host: net.JoinHostPort(ip, strconv.Itoa(int(port))), Path: endpoint.Path}, false, nil
}
