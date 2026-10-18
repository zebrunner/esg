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
	"github.com/zebrunner/esg/service"
)

func taskStopped(ctx context.Context, taskID string) bool {
	result, err := service.DescribeTask(ctx, taskID)
	return err == nil && len(result.Tasks) == 1 && aws.ToString(result.Tasks[0].LastStatus) == "STOPPED"
}

func recorderURL(ctx context.Context, entity *mapper.Mapper) (*url.URL, bool, error) {
	address := func(ip string, port int64) (*url.URL, bool, error) {
		if parsed := net.ParseIP(ip); parsed == nil || !parsed.IsPrivate() || port <= 0 || port > 65535 {
			return nil, false, fmt.Errorf("private recorder address unavailable")
		}
		return &url.URL{Scheme: "http", Host: net.JoinHostPort(ip, strconv.FormatInt(port, 10)), Path: "/monitoring/v1/summary"}, false, nil
	}
	if endpoint := entity.Network.Endpoints["recorderStop"]; endpoint != nil && endpoint.HostPort > 0 {
		if ip := net.ParseIP(entity.Network.IP); ip != nil && ip.IsPrivate() {
			return address(entity.Network.IP, endpoint.HostPort)
		}
	}
	result, err := service.DescribeTask(ctx, entity.TaskId)
	if err != nil {
		return nil, false, err
	}
	if len(result.Tasks) != 1 {
		return nil, false, fmt.Errorf("known task unavailable")
	}
	task := result.Tasks[0]
	if aws.ToString(task.LastStatus) == "STOPPED" {
		return nil, true, nil
	}
	var port int32
	for _, container := range task.Containers {
		if aws.ToString(container.Name) != "recorder" {
			continue
		}
		for _, binding := range container.NetworkBindings {
			if aws.ToInt32(binding.ContainerPort) == 9080 {
				port = aws.ToInt32(binding.HostPort)
			}
		}
		if port == 0 {
			for _, nic := range container.NetworkInterfaces {
				if ip := aws.ToString(nic.PrivateIpv4Address); ip != "" {
					return address(ip, 9080)
				}
			}
		}
	}
	if port <= 0 || task.ContainerInstanceArn == nil {
		return nil, false, fmt.Errorf("recorder binding unavailable")
	}
	instances, err := service.DescribeContainerInstances(ctx, []string{aws.ToString(task.ContainerInstanceArn)}, ecs.NewFromConfig(service.AwsCfg))
	if err != nil {
		return nil, false, err
	}
	if len(instances) != 1 || instances[0].Ec2InstanceId == nil {
		return nil, false, fmt.Errorf("container instance unavailable")
	}
	hosts, err := service.DescribeInstances(ctx, []string{aws.ToString(instances[0].Ec2InstanceId)}, ec2.NewFromConfig(service.AwsCfg))
	if err != nil {
		return nil, false, err
	}
	if len(hosts) != 1 {
		return nil, false, fmt.Errorf("recorder host unavailable")
	}
	return address(aws.ToString(hosts[0].PrivateIpAddress), int64(port))
}
