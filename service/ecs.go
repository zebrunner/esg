package service

import (
	"context"
	"fmt"

	//	"github.com/docker/go-units"
	"log"
	"math/rand"
	"net/url"
	"strconv"
	"time"

	"github.com/aerokube/selenoid/session"
	"github.com/aerokube/util"

	//	ctr "github.com/docker/docker/api/types/container"
	//	"github.com/docker/docker/client"

	//	"os"
	//	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"

	"github.com/google/uuid"
)

// Task - ecs task container manager
type Task struct {
	ServiceBase
	Environment
	session.Caps
}

type ecsPortConfig struct {
	SeleniumPort   int64
	FileserverPort int64
	ClipboardPort  int64
	DevtoolsPort   int64
	VNCPort        int64
}

// StartWithCancel - Starter interface implementation
func (d *Task) StartWithCancel() (*StartedService, error) {
	requestId := d.RequestId

	portConfig, err := getEcsPortConfig()
	if err != nil {
		return nil, fmt.Errorf("configuring ports: %v", err)
	}
	ctx := context.Background()
	/*	log.Printf("[%d] [CREATING_CONTAINER] [%s]", requestId, image)
		hostConfig := ctr.HostConfig{
			ExtraHosts: getExtraHosts(d.Service, d.Caps),
		}

		if len(d.Caps.DNSServers) > 0 {
			hostConfig.DNS = d.Caps.DNSServers
		}
	*/

	memory, memErr := getEcsMemory(d.Caps)
	memoryReservation, memResErr := getEcsMemoryReservation(d.Caps)
	cpu, cpuErr := getEcsCpu(d.Caps)
	if memErr != nil || memResErr != nil || cpuErr != nil {
		return nil, fmt.Errorf("error happend while parsing resources. Errors: [%v, %v, %v]", memErr, memResErr, cpuErr)
	}

	imageUrl := d.Service.Image

	// Without unique nano postfix we face with AWS limitations during multi-threading execution a lot...
	taskDefFamily := d.Caps.Name + "-" + strconv.Itoa(int(time.Now().UnixNano()))
	log.Printf("[%d] [TASK_DEFINITION_FAMILY] [%s]", requestId, taskDefFamily)

	//create ECS task definition based on capabilities
	svc := ecs.New(awsSession.New(&aws.Config{Region: &AwsRegion, MaxRetries: &AwsRetry}))

	//TODO: support GPU reservation: The number of GPU units to reserve for the container. A container instance with GPU support has 1 GPU unit for every GPU.
	log.Printf("[%d] [CREATING_ECS_TASK_DEFINITION] [%s]", requestId, imageUrl)

	uuid := uuid.New()

	videoFileName := uuid.String() + ".mp4"

	sharedFolder := "/tmp/log"
	sharedVolume := "data"

	// [VD]that's expected that inside chrome container no uuid for folder
	driverArgs := "--log-path=/tmp/log/" + uuid.String() + ".log"
	log.Printf("driverArgs: %s", driverArgs)

	browserContainerName := "browser"
	taskDefinitionInput := &ecs.RegisterTaskDefinitionInput{
		NetworkMode: aws.String("bridge"),
		ContainerDefinitions: []*ecs.ContainerDefinition{
			{
				Name:              aws.String(browserContainerName),
				Image:             aws.String(imageUrl),
				Cpu:               &cpu,
				Essential:         aws.Bool(true), //If the essential parameter of a container is marked as true, the failure of that container will stop the task.
				Memory:            &memory,
				MemoryReservation: &memoryReservation,
				Privileged:        aws.Bool(true), //privileged mode is needed to start browser driver correctly
				MountPoints: []*ecs.MountPoint{
					&ecs.MountPoint{
						ContainerPath: aws.String("/dev/shm"),
						ReadOnly:      aws.Bool(false),
						SourceVolume:  aws.String("devshm"),
					},
					&ecs.MountPoint{
						ContainerPath: aws.String("/tmp/log"),
						ReadOnly:      aws.Bool(false),
						SourceVolume:  aws.String(sharedVolume),
					},
				},
				Environment: []*ecs.KeyValuePair{
					//TODO: provide extra values from caps
					&ecs.KeyValuePair{
						Name:  aws.String("VERBOSE"),
						Value: aws.String("1"),
					},
					&ecs.KeyValuePair{
						Name:  aws.String("DRIVER_ARGS"),
						Value: aws.String(driverArgs),
					},
				},
				PortMappings: []*ecs.PortMapping{
					&ecs.PortMapping{
						ContainerPort: aws.Int64(d.Service.Port),
						HostPort:      aws.Int64(portConfig.SeleniumPort),
					},
					&ecs.PortMapping{
						ContainerPort: aws.Int64(5900),
						HostPort:      aws.Int64(portConfig.VNCPort),
					},
					&ecs.PortMapping{
						ContainerPort: aws.Int64(7070),
						HostPort:      aws.Int64(portConfig.DevtoolsPort),
					},
					&ecs.PortMapping{
						ContainerPort: aws.Int64(8080),
						HostPort:      aws.Int64(portConfig.FileserverPort),
					},
					&ecs.PortMapping{
						ContainerPort: aws.Int64(9090),
						HostPort:      aws.Int64(portConfig.ClipboardPort),
					},
				},
			},
			{
				Name:              aws.String("video-recorder"),
				Image:             aws.String("selenoid/video-recorder:latest-release"),
				Essential:         aws.Bool(false), //If the essential parameter of a container is marked as true, the failure of that container will stop the task.
				Cpu:               aws.Int64(256),
				Memory:            aws.Int64(768),
				MemoryReservation: aws.Int64(768),
				Privileged:        aws.Bool(false), //no need privileged mode for video-recording container
				Links: []*string{
					aws.String(browserContainerName),
				},
//                                DependsOn: []*ecs.ContainerDependency{
//                                        &ecs.ContainerDependency{
//                                                ContainerName: aws.String(browserContainerName),
//                                                Condition: aws.String("START"),
//                                        },
//                                },
				Environment: []*ecs.KeyValuePair{
					//TODO: provide extra values from caps
					&ecs.KeyValuePair{
						Name:  aws.String("BROWSER_CONTAINER_NAME"),
						Value: aws.String(browserContainerName),
					},
					&ecs.KeyValuePair{
						Name:  aws.String("FILE_NAME"),
						Value: aws.String(videoFileName),
					},
				},
				MountPoints: []*ecs.MountPoint{
					&ecs.MountPoint{
						ContainerPath: aws.String("/data"),
						ReadOnly:      aws.Bool(false),
						SourceVolume:  aws.String(sharedVolume),
					},
				},
				PortMappings: []*ecs.PortMapping{},
			},
		},

		Family: aws.String(taskDefFamily),
		Volumes: []*ecs.Volume{
			&ecs.Volume{
				Host: &ecs.HostVolumeProperties{
					SourcePath: aws.String("/dev/shm"),
				},
				Name: aws.String("devshm"),
			},
			&ecs.Volume{
				Host: &ecs.HostVolumeProperties{
					SourcePath: aws.String(sharedFolder),
				},
				Name: aws.String(sharedVolume),
			},
		},
		TaskRoleArn: aws.String(""),
	}

	time.Sleep(1 * time.Second)
	resultTaskDefinition, err := svc.RegisterTaskDefinition(taskDefinitionInput)
	if err != nil {
		return nil, fmt.Errorf("Unable to create task definition: %v", err)
		//	} else {
		//            log.Printf("[%d] [TASK_DEFINITION] [%s]", requestId, resultTaskDefinition)
	}

	//	taskDefinition := resultTaskDefinition.TaskDefinition
	//        log.Printf("[%d] [TASK_DEFINITION] [%s]", requestId, taskDefinition)

	taskStartTime := time.Now()
	log.Printf("[%d] [STARTING_TASK] [%s] [%s]", requestId, imageUrl, taskStartTime)

	family := *resultTaskDefinition.TaskDefinition.Family
	revision := *resultTaskDefinition.TaskDefinition.Revision

	// Pass a context with a timeout to tell a blocking function that it should abandon its work after the timeout elapses.
	//TODO: parametrize provision timeout
	/*
		provisionTimeout := 60 * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout)
		defer cancel()

		select {
		case <-time.After(10 * time.Second):
			fmt.Println("overslept 60...")
		case <-ctx.Done():
			fmt.Println(ctx.Err()) // prints "context deadline exceeded"
		}

	*/
	runTaskInput := &ecs.RunTaskInput{
		Cluster:        &AwsCluster,
		TaskDefinition: aws.String(family + ":" + strconv.FormatInt(revision, 10)),
	}

	resultRunTask, err := svc.RunTask(runTaskInput)
	/*
	   if err != nil {
	       return nil, fmt.Errorf("Unable to run task: %v", err)
	   } else {
	       log.Printf("[%d] [TASK_RUN] [%s]", requestId, resultRunTask)
	   }
	*/

	taskFailure := ""
	for retry := 1; retry < 5; retry++ {
		if err != nil {
			log.Printf("[%d] [TASK_RUN_ERROR] [%s] [%d]", requestId, err, retry)
		} else if len(resultRunTask.Failures) > 0 {
			taskFailure = *resultRunTask.Failures[0].Reason
			log.Printf("[%d] [TASK_RUN_FAILURE] [%s] [%d]", requestId, taskFailure, retry)
		} else {
			// all good and we can proceed
			taskFailure = "" //reset taskFailure if any
			break
		}

		// retry run task operation
		time.Sleep(5 * time.Second)
		resultRunTask, err = svc.RunTask(runTaskInput)
	}

	//TODO: add task definition removal for negative use-case
	if err != nil {
		return nil, fmt.Errorf("Unable to run task: %v", err)
	}
	if taskFailure != "" {
		return nil, fmt.Errorf("Unable to run task: %s", taskFailure)
	}

	/*
		Failures: [{
		      Arn: "arn:aws:ecs:us-east-1:659932254483:container-instance/829954d05541417cb21d02409e43ea10",
		      Reason: "RESOURCE:CPU"
		    }],
		  Tasks: []
		}]
	*/

	// [TASK_ARN] [arn:aws:ecs:us-east-1:659932254483:task/executor-cluster/35bab349ee55458e9182b84b999dbd1c]
	taskArn := *resultRunTask.Tasks[0].TaskArn
	log.Printf("[%d] [TASK_ARN] [%s]", requestId, taskArn)
	taskId := strings.Split(taskArn, "/")[2]
	//	log.Printf("[%d] [TASK_ID] [%s]", requestId, taskId)

	time.Sleep(1 * time.Second)
	//	time.Sleep(5 * time.Second) //TODO: organize valid waiter using startup-timeout until task is RUNNING
	// TASK DESCRIBE contains information about actual host/port bindings. Potentially we could user taskId to setup stateless mapping
	/*
	   {
	     BindIP: "0.0.0.0",
	     ContainerPort: 4444,
	     HostPort: 4444,
	     Protocol: "tcp"
	   },
	*/
	//TODO: wait until container starts (in response we should have valid *resultRunTask.Tasks[0].ContainerInstanceArn value
	describeTaskInput := &ecs.DescribeTasksInput{
		Cluster: &AwsCluster,
		Tasks: []*string{
			aws.String(taskId),
		},
	}
	time.Sleep(15 * time.Second)
	// Check if task is in provisioning in running or provisioning task
	ScaleUp()

	err = svc.WaitUntilTasksRunning(describeTaskInput)
	if err != nil {
		RemoveTask(ctx, requestId, taskArn)
		failReason, reasonErr := getFailReason(svc, taskId)
		if reasonErr == nil {
			return nil, fmt.Errorf("Unable to wait until task is running: %v", *failReason)
		} else {
			return nil, fmt.Errorf("Unable to wait until task is running: %v", err)
		}
	}

	resultDescribeTask, err := svc.DescribeTasks(describeTaskInput)
	if err != nil {
		RemoveTask(ctx, requestId, taskArn)
		return nil, fmt.Errorf("Unable to describe task: %v", err)
		//        } else {
		//            log.Printf("[%d] [TASK_DESCRIBE] [%s]", requestId, resultDescribeTask)
	}

	// [TASK_CONTAINER_INSTANCE] [arn:aws:ecs:us-east-1:659932254483:container-instance/executor-cluster/bf3d12885ef243f2961e88d72baa0f77]
	containerInstanceArn := *resultDescribeTask.Tasks[0].ContainerInstanceArn

	log.Printf("[%d] [TASK_CONTAINER_INSTANCE] [%s]", requestId, containerInstanceArn)
	containerInstanceId := strings.Split(containerInstanceArn, "/")[2]
	log.Printf("[%d] [TASK_CONTAINER_INSTANCE_ID] [%s]", requestId, containerInstanceId)

	containerInstanceInput := &ecs.DescribeContainerInstancesInput{
		Cluster: &AwsCluster,
		ContainerInstances: []*string{
			aws.String(containerInstanceId),
		},
	}
	resultContainerInstance, err := svc.DescribeContainerInstances(containerInstanceInput)
	if err != nil {
		RemoveTask(ctx, requestId, taskArn)
		return nil, fmt.Errorf("Unable to get container instance details: %v", err)
		//        } else {
		//           log.Printf("[%d] [TASK_CONTAINER_INSTANCE_DETAILS] [%s]", requestId, resultContainerInstance)
	}

	//TODO: verify that returned number of instances is 1!
	instanceId := *resultContainerInstance.ContainerInstances[0].Ec2InstanceId
	log.Printf("[%d] [INSTANCE_ID] [%s]", requestId, instanceId)

	//    fmt.Println("[AWS RESPONSE]", resultContainerInstance.ContainerInstances[0])

	instanceInput := &ec2.DescribeInstancesInput{
		InstanceIds: []*string{
			aws.String(instanceId),
		},
	}

	svcEc2 := ec2.New(awsSession.New(&aws.Config{Region: &AwsRegion}))
	resultInstance, err := svcEc2.DescribeInstances(instanceInput)
	if err != nil {
		RemoveTask(ctx, requestId, taskArn)
		return nil, fmt.Errorf("Unable to get instance details: %v", err)
		//        } else {
		//           log.Printf("[%d] [TASK_INSTANCE_DETAILS] [%s]", requestId, resultInstance)
	}
	privateIpAddress := *resultInstance.Reservations[0].Instances[0].PrivateIpAddress
	log.Printf("[%d] [INSTANCE_PRIVATE_IP] [%s]", requestId, privateIpAddress)
	publicIpAddress := *resultInstance.Reservations[0].Instances[0].PublicIpAddress
	log.Printf("[%d] [INSTANCE_PUBLIC_IP] [%s]", requestId, publicIpAddress)

	browserTaskStartTime := time.Now()
	log.Printf("[%d] [TASK_STARTED] [%s] [%s] [%.2fs]", requestId, imageUrl, taskId, util.SecondsSince(browserTaskStartTime))

	hostPort := getTaskHostPort(d.Caps, privateIpAddress, portConfig)
	log.Printf("[%d] [HOST_PORT] [%s]", requestId, hostPort)

	u := &url.URL{Scheme: "http", Host: hostPort.Selenium, Path: d.Service.Path}
	log.Printf("[%d] [CONTAINER_SERVICE_URL] [%s]", requestId, u)

	serviceStartTime := time.Now()
	err = wait(u.String(), d.StartupTimeout)
	if err != nil {
		RemoveTask(ctx, requestId, taskArn)
		return nil, fmt.Errorf("wait: %v", err)
	}
	log.Printf("[%d] [SERVICE_STARTED] [%s] [%s] [%.2fs]", requestId, imageUrl, taskId, util.SecondsSince(serviceStartTime))
	log.Printf("[%d] [PROXY_TO] [%s] [%s]", requestId, taskId, u.String())

	// publish all ports feature is still under question for ecs task service so empty map is ok
	var publishedPortsInfo map[string]string

	s := StartedService{
		Url: u,
		Container: &session.Container{
			ID:                  taskId,
			ContainerInstanceID: containerInstanceId,
			IPAddress:           privateIpAddress,
			Ports:               publishedPortsInfo,
		},
		TaskID:   taskId,
		HostPort: hostPort,
		Cancel: func() {
			RemoveTask(ctx, requestId, taskArn)
                        UploadArtifacts(uuid.String(), sharedFolder, sharedVolume)
		},
	}

	log.Printf("[%d] [TASK_SERVICE_DETAILS] [%s]", requestId, s)
	return &s, nil
}

func getFailReason(svc *ecs.ECS, taskId string) (*string, error) {
	describeTaskInput := &ecs.DescribeTasksInput{
		Cluster: &AwsCluster,
		Tasks:   []*string{aws.String(taskId)},
	}
	describeTaskResult, err := svc.DescribeTasks(describeTaskInput)
	if err != nil {
		return nil, err
	}

	resultReason := ""
	for _, container := range describeTaskResult.Tasks[0].Containers {
		if container.Reason != nil {
			resultReason += *container.Reason
		}
	}

	return &resultReason, nil
}

func GetTaskInfo(instanceID string, taskID string) {
	svc := ecs.New(awsSession.New(&aws.Config{Region: &AwsRegion, MaxRetries: &AwsRetry}))
	input := &ecs.ListTasksInput{
		Cluster:           &AwsCluster,
		ContainerInstance: aws.String(instanceID),
	}
	fmt.Println(instanceID)
	result, err := svc.ListTasks(input)
	if err != nil {
		log.Printf("[GET TASK INFO ERROR] %v", err)
	} else {
		fmt.Println("TASK LIST RESULT", result)
	}
}

func getEcsPortConfig() (*ecsPortConfig, error) {
	//TODO: implement unique ports generation maybe as external service/lambda to support stateless ecs-docker service
	selelinumPort := rand.Int63n(64511) + 1025
	fileserverPort := rand.Int63n(64511) + 1025
	clipboardPort := rand.Int63n(64511) + 1025
	vncPort := rand.Int63n(64511) + 1025
	devtoolsPort := rand.Int63n(64511) + 1025
	return &ecsPortConfig{
		SeleniumPort:   selelinumPort,
		FileserverPort: fileserverPort,
		ClipboardPort:  clipboardPort,
		VNCPort:        vncPort,
		DevtoolsPort:   devtoolsPort}, nil
}

func parseResourceCapability(cap string, defaultValue int, capabilityName string) (int, error) {
	if cap == "" {
		return defaultValue, nil
	}
	resource, err := strconv.Atoi(cap)
	if err != nil {
		return 0, fmt.Errorf("unexpected %s capability format. %v Expected integer, got: %v", capabilityName, err, cap)
	}
	return resource, nil
}

func getEcsMemory(caps session.Caps) (int64, error) {
	memory, err := parseResourceCapability(caps.Memory, MinMemory, "Memory")
	if err != nil {
		return 0, err
	}
	if memory < MinMemory {
		fmt.Println("[WARN] Requested Memory is lower than MinMemory. Using MinMemory as default. Requested:", memory, "MinMemory:", MinMemory)
		return int64(MinMemory), nil
	} else if memory > MaxMemory {
		return 0, fmt.Errorf("Requested Memory is grater than MaxMemory allowed by system administrator. Requested: %d. MaxMemory: %d", memory, MaxMemory)
	}
	return int64(memory), nil
}

func getEcsMemoryReservation(caps session.Caps) (int64, error) {
	memoryReservation, err := parseResourceCapability(caps.MemoryReservation, MinMemoryReservation, "MemoryReservation")
	if err != nil {
		return 0, err
	}
	if memoryReservation < MinMemoryReservation {
		fmt.Println("[WARN] Requested MemoryReservation lower than MinMemoryReservation. Using MinMemoryReservation as default. Requested:", memoryReservation, "MinMemoryReservation:", MinMemoryReservation)
		return int64(MinMemoryReservation), nil
	} else if memoryReservation > MaxMemoryReservation {
		return 0, fmt.Errorf("Requested MemoryReservation is grater than MaxMemoryReservation allowed by system administrator. Requested: %d. MaxMemory: %d", memoryReservation, MaxMemoryReservation)
	}
	return int64(memoryReservation), nil
}

func getEcsCpu(caps session.Caps) (int64, error) {
	cpu, err := parseResourceCapability(caps.Cpu, MinCpu, "Cpu")
	if err != nil {
		return 0, err
	}
	if cpu < MinCpu {
		fmt.Println("[WARN] Requested CPU lower than MinCpu. Using MinCpu as default. Requested:", cpu, "MinCpu:", MinCpu)
		return int64(MinCpu), nil
	} else if cpu > MaxCpu {
		return 0, fmt.Errorf("Requested CPU is grater than MaxCpu allowed by system administrator. Requested: %d. MaxCpu: %d", cpu, MaxCpu)
	}
	return int64(cpu), nil
}

func getTaskHostPort(caps session.Caps, taskIP string, pc *ecsPortConfig) session.HostPort {
	fn := func(containerPort int64) string {
		return ""
	}
	containerIP := taskIP
	fn = func(containerPort int64) string {
		return containerIP + ":" + strconv.FormatInt(containerPort, 10)
	}

	hp := session.HostPort{
		Selenium:   fn(pc.SeleniumPort),
		Fileserver: fn(pc.FileserverPort),
		Clipboard:  fn(pc.ClipboardPort),
		Devtools:   fn(pc.DevtoolsPort),
	}

	if caps.VNC {
		hp.VNC = fn(pc.VNCPort)
	}

	return hp
}

func RemoveTask(ctx context.Context, requestId uint64, taskArn string) {
	log.Printf("[%d] [REMOVING_TASK] [%s]", requestId, taskArn)

	//TODO: parametrize region
	// #33: increased number of retries to fix "ThrottlingException: Rate exceeded"
	svc := ecs.New(awsSession.New(&aws.Config{Region: &AwsRegion, MaxRetries: &AwsRetry}))

	stopTaskInput := &ecs.StopTaskInput{
		Cluster: &AwsCluster,
		Reason:  aws.String("Cancel"),
		Task:    aws.String(taskArn),
	}

	resultStopTask, err := svc.StopTask(stopTaskInput)
	if err != nil {
		log.Printf("[%d] [FAILED_TO_STOP_TASK] [%s] [%v]", requestId, taskArn, err)
		return
	}
	taskDefinitionArn := *resultStopTask.Task.TaskDefinitionArn

	taskDeregisterInput := &ecs.DeregisterTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefinitionArn),
	}
	resultTaskDeregister, err := svc.DeregisterTaskDefinition(taskDeregisterInput)
	if err != nil {
		log.Printf("[%d] [FAILED_TO_DEREGISTER_TASK_DEFINITION] [%s] [%v]", requestId, taskDefinitionArn, err)
		return
	} else {
		log.Printf("[%d] [TASK_DEFINITION_REMOVED] [%s]", requestId, *resultTaskDeregister.TaskDefinition.TaskDefinitionArn)
	}
}


func UploadArtifacts(uuid string, sharedFolder string, sharedVolume string) {
	//TODO: verify that AWS S3 integration parameters are available and exit if not

        log.Printf("S3_UPLOADER for session: [%s]", uuid)
        //create ECS task definition based on capabilities
        svc := ecs.New(awsSession.New(&aws.Config{Region: &AwsRegion, MaxRetries: &AwsRetry}))
        taskDefinitionInput := &ecs.RegisterTaskDefinitionInput{
                NetworkMode: aws.String("bridge"),
                ContainerDefinitions: []*ecs.ContainerDefinition{
                             {
                                Name:              aws.String("s3-cli"),
                                Image:             aws.String("zebrunner/s3-cli"),
                                Essential:         aws.Bool(true), //If the essential parameter of a container is marked as true, the failure of that container will stop the task.
                                Cpu:               aws.Int64(128),
                                Memory:            aws.Int64(128),
                                MemoryReservation: aws.Int64(128),
                                Privileged:        aws.Bool(false), //no need privileged mode for video-recording container
                                Environment: []*ecs.KeyValuePair{
                                        &ecs.KeyValuePair{
                                                Name:  aws.String("UUID"),
                                                Value: aws.String(uuid),
                                        },
                                        &ecs.KeyValuePair{
                                                Name:  aws.String("LOG_FOLDER"),
                                                Value: aws.String(sharedFolder),
                                        },
                                        &ecs.KeyValuePair{
                                                Name:  aws.String("BUCKET"),
                                                Value: aws.String(""),
                                        },
                                        &ecs.KeyValuePair{
                                                Name:  aws.String("TENANT"),
                                                Value: aws.String(""),
                                        },
                                        &ecs.KeyValuePair{
                                                Name:  aws.String("AWS_ACCESS_KEY_ID"),
                                                Value: aws.String(""),
                                        },
                                        &ecs.KeyValuePair{
                                                Name:  aws.String("AWS_SECRET_ACCESS_KEY"),
                                                Value: aws.String(""),
                                        },
                                        &ecs.KeyValuePair{
                                                Name:  aws.String("AWS_DEFAULT_REGION"),
                                                Value: &AwsRegion,
                                        },

                                },
                                MountPoints: []*ecs.MountPoint{
                                        &ecs.MountPoint{
                                                ContainerPath: aws.String("/tmp/log"),
                                                ReadOnly:      aws.Bool(false),
                                                SourceVolume:  aws.String(sharedVolume),
                                        },
                                },
                                PortMappings: []*ecs.PortMapping{},
                        },
					},
                Family: aws.String("s3-cli"),
                Volumes: []*ecs.Volume{
                        &ecs.Volume{
                                Host: &ecs.HostVolumeProperties{
                                        SourcePath: aws.String(sharedFolder),
                                },
                                Name: aws.String(sharedVolume),
                        },
                },
                TaskRoleArn: aws.String(""),
        }

        resultTaskDefinition, err := svc.RegisterTaskDefinition(taskDefinitionInput)
        if err != nil {
		fmt.Errorf("Unable to create s3-cli task definition: %v", err)
                return
        }

        family := *resultTaskDefinition.TaskDefinition.Family
        revision := *resultTaskDefinition.TaskDefinition.Revision

        runTaskInput := &ecs.RunTaskInput{
                Cluster:        &AwsCluster,
                TaskDefinition: aws.String(family + ":" + strconv.FormatInt(revision, 10)),
        }

        resultRunTask, err := svc.RunTask(runTaskInput)
        if err != nil {
		fmt.Errorf("Unable to run task: %v", err)
               return
        }

        log.Printf("S3_UPLOADER for session: [%s]; %v", uuid, resultRunTask)
	return
}

