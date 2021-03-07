package service

import (
	"context"
	"fmt"
//	"github.com/docker/go-units"
	"log"
	"net/url"
	"strconv"
	"time"
	"math/rand"

	"github.com/aerokube/selenoid/session"
	"github.com/aerokube/util"
	ctr "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

//	"os"
//	"path/filepath"
	"strings"

        "github.com/aws/aws-sdk-go/aws"
        "github.com/aws/aws-sdk-go/service/ecs"
        "github.com/aws/aws-sdk-go/service/ec2"
        awsSession "github.com/aws/aws-sdk-go/aws/session"
)

// Task - ecs task container manager
type Task struct {
       ServiceBase
       Environment
       session.Caps
       LogConfig *ctr.LogConfig
       Client    *client.Client
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
		LogConfig:    getLogConfig(*d.LogConfig, d.Caps),
		Tmpfs:        d.Service.Tmpfs,
		ExtraHosts: getExtraHosts(d.Service, d.Caps),
	}

	hostConfig.PublishAllPorts = d.Service.PublishAllPorts
	if len(d.Caps.DNSServers) > 0 {
		hostConfig.DNS = d.Caps.DNSServers
	}
	if len(d.ApplicationContainers) > 0 {
		hostConfig.Links = d.ApplicationContainers
	}
	if len(d.Service.Sysctl) > 0 {
		hostConfig.Sysctls = d.Service.Sysctl
	}
*/

        hardMemory, softMemory := getEcsMemory(d.Caps)
        cpu := getEcsCpu(d.Caps)
	imageUrl := getEcsImage(d.Caps)

	// Without unique nano postfix we face with AWS limitations during multi-threading execution a lot...
	taskDefFamily := d.Caps.Name + "-" + strconv.Itoa(int(time.Now().UnixNano()))
	log.Printf("[%d] [TASK_DEFINITION_FAMILY] [%s]", requestId, taskDefFamily)

	//create ECS task definition based on capabilities
	//TODO: parametrize region
//	config := &aws.Config{Region: aws.String("us-east-1"), MaxRetries: aws.Int(15)}
//	config.WithSleepDelay(time.Sleep)
//	svc := ecs.New(awsSession.New(config))
	svc := ecs.New(awsSession.New(&aws.Config{Region: aws.String("us-east-1"), MaxRetries: aws.Int(10)}))

	//TODO: support GPU reservation: The number of GPU units to reserve for the container. A container instance with GPU support has 1 GPU unit for every GPU.
        log.Printf("[%d] [CREATING_ECS_TASK_DEFINITION] [%s]", requestId, imageUrl)
	taskDefinitionInput := &ecs.RegisterTaskDefinitionInput{
            NetworkMode: aws.String("bridge"),
	    ContainerDefinitions: []*ecs.ContainerDefinition{
	        {
                    Name:      aws.String(d.Caps.Name),
                    Image:     aws.String(imageUrl),
	            Cpu:       aws.Int64(cpu),
	            Essential: aws.Bool(true), //If the essential parameter of a container is marked as true, the failure of that container will stop the task.
	            Memory:    aws.Int64(hardMemory),
                    MemoryReservation: aws.Int64(softMemory),
		    Privileged: aws.Bool(d.Privileged),
		    MountPoints: []*ecs.MountPoint{
			&ecs.MountPoint{
			    ContainerPath: aws.String("/dev/shm"),
	                    ReadOnly:      aws.Bool(false),
	                    SourceVolume:  aws.String("devshm"),
	                },
                        &ecs.MountPoint{
                            ContainerPath: aws.String("/tmp/zebrunner/logs"),
                            ReadOnly:      aws.Bool(false),
                            SourceVolume:  aws.String("data"),
                        },
	            },
                    Environment: []*ecs.KeyValuePair{
			//TODO: provide extra values from caps
			&ecs.KeyValuePair{
			    Name: aws.String("VERBOSE"),
			    Value: aws.String("1"),
			},
		    },
	            PortMappings: []*ecs.PortMapping{
			&ecs.PortMapping{
			    ContainerPort: aws.Int64(4444),
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
                    Name:      aws.String("video-recorder"),
                    Image:     aws.String("selenoid/video-recorder:latest-release"),
                    Essential: aws.Bool(true), //If the essential parameter of a container is marked as true, the failure of that container will stop the task.
                    Cpu:       aws.Int64(256),
                    Memory:    aws.Int64(512),
                    MemoryReservation: aws.Int64(512),
                    Privileged: aws.Bool(d.Privileged),
                    Links: []*string{
                        aws.String(d.Caps.Name),
                    },
                    Environment: []*ecs.KeyValuePair{
			//TODO: provide extra values from caps
			&ecs.KeyValuePair{
			    Name: aws.String("BROWSER_CONTAINER_NAME"),
			    Value: aws.String(d.Caps.Name),
			},
                        &ecs.KeyValuePair{
                            Name: aws.String("FILE_NAME"),
                            Value: aws.String(d.Caps.VideoName),
                        },
		    },
                    MountPoints: []*ecs.MountPoint{
                        &ecs.MountPoint{
                            ContainerPath: aws.String("/data"),
                            ReadOnly:      aws.Bool(false),
                            SourceVolume:  aws.String("data"),
                        },
                    },
                    PortMappings: []*ecs.PortMapping{
                    },
                },
	    },
	    Family:      aws.String(taskDefFamily),
	    Volumes: []*ecs.Volume{
	        &ecs.Volume{
	            Host: &ecs.HostVolumeProperties{
	                SourcePath: aws.String("/dev/shm"),
	            },
	            Name: aws.String("devshm"),
	        },
                &ecs.Volume{
                    Host: &ecs.HostVolumeProperties{
                        SourcePath: aws.String("/tmp/zebrunner/video"),
                    },
                    Name: aws.String("data"),
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
	//TODO: parametrize cluster name 
	runTaskInput := &ecs.RunTaskInput{
	    Cluster:        aws.String("executor-cluster"),
	    TaskDefinition: aws.String(family + ":" + strconv.FormatInt(revision, 10)),
	}

        resultRunTask, err := svc.RunTask(runTaskInput)
        if err != nil {
            return nil, fmt.Errorf("Unable to run task: %v", err)
        } else {
            log.Printf("[%d] [TASK_RUN] [%s]", requestId, resultRunTask)
        }

	//log.Printf("size of the tasks: [%s]", len(resultRunTask.Tasks))
        //log.Printf("size of the failures: [%s]", len(resultRunTask.Failures))

	// Handle not enough CPU failure doing extra run task call
	if len(resultRunTask.Failures) > 0 {
		taskFailure := *resultRunTask.Failures[0].Reason
	        log.Printf("failure reason: %s", taskFailure)
		time.Sleep (5 * time.Second)
		//try to start one more time
	        resultRunTask, err = svc.RunTask(runTaskInput)
	        if err != nil {
	            return nil, fmt.Errorf("Unable to run task: %v", err)
	        } else {
	            log.Printf("[%d] [TASK_RUN] [%s]", requestId, resultRunTask)
        	}
	        //log.Printf("size of the tasks: [%s]", len(resultRunTask.Tasks))
	        //log.Printf("size of the failures: [%s]", len(resultRunTask.Failures))
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
        log.Printf("[%d] [TASK_ID] [%s]", requestId, taskId)


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
           Cluster:           aws.String("executor-cluster"),
            Tasks: []*string{
                aws.String(taskId),
            },
        }
	err = svc.WaitUntilTasksRunning(describeTaskInput)
        if err != nil {
            removeTask(ctx, requestId, taskArn)
            return nil, fmt.Errorf("Unable to wait until task is running: %v", err)
        }

        resultDescribeTask, err := svc.DescribeTasks(describeTaskInput)
        if err != nil {
            removeTask(ctx, requestId, taskArn)
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
	    Cluster: aws.String("executor-cluster"),
	    ContainerInstances: []*string{
	        aws.String(containerInstanceId),
	    },
	}
	resultContainerInstance, err := svc.DescribeContainerInstances(containerInstanceInput)
        if err != nil {
	    removeTask(ctx, requestId, taskArn)
            return nil, fmt.Errorf("Unable to get container instance details: %v", err)
//        } else {
//           log.Printf("[%d] [TASK_CONTAINER_INSTANCE_DETAILS] [%s]", requestId, resultContainerInstance)
        }

	//TODO: verify that returned number of instances is 1!
    instanceId := *resultContainerInstance.ContainerInstances[0].Ec2InstanceId
    log.Printf("[%d] [INSTANCE_ID] [%s]", requestId, instanceId)
    fmt.Println("[AWS RESPONSE]", resultContainerInstance.ContainerInstances[0])

	instanceInput := &ec2.DescribeInstancesInput{
	    InstanceIds: []*string{
	        aws.String(instanceId),
	    },
	}

	svcEc2 := ec2.New(awsSession.New(&aws.Config{Region: aws.String("us-east-1")}))
	resultInstance, err := svcEc2.DescribeInstances(instanceInput)
        if err != nil {
	    removeTask(ctx, requestId, taskArn)
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
		removeTask(ctx, requestId, taskArn)
		return nil, fmt.Errorf("wait: %v", err)
	}
	log.Printf("[%d] [SERVICE_STARTED] [%s] [%s] [%.2fs]", requestId, imageUrl, taskId, util.SecondsSince(serviceStartTime))
	log.Printf("[%d] [PROXY_TO] [%s] [%s]", requestId, taskId, u.String())

	// publish all ports feature is still under question for ecs task service so empty map is ok
        var publishedPortsInfo map[string]string

	s := StartedService{
		Url: u,
	        Container: &session.Container{
	                ID:        taskId,
	                ContainerInstanceID: containerInstanceId,
	                IPAddress: privateIpAddress,
	                Ports:     publishedPortsInfo,
	        },
		HostPort: hostPort,
		Cancel: func() {
			removeTask(ctx, requestId, taskArn)
			//TODO: review old functionality and do extra cleanup if needed
/*
			if d.LogOutputDir != "" && (d.SaveAllLogs || d.Log) {
				r, err := d.Client.ContainerLogs(ctx, browserContainerId, types.ContainerLogsOptions{
					Timestamps: true,
					ShowStdout: true,
					ShowStderr: true,
				})
				if err != nil {
					log.Printf("[%d] [FAILED_TO_COPY_LOGS] [%s] [Failed to capture container logs: %v]", requestId, browserContainerId, err)
					return
				}
				defer r.Close()
				filename := filepath.Join(d.LogOutputDir, d.LogName)
				f, err := os.Create(filename)
				if err != nil {
					log.Printf("[%d] [FAILED_TO_COPY_LOGS] [%s] [Failed to create log file %s: %v]", requestId, browserContainerId, filename, err)
					return
				}
				defer f.Close()
				_, err = stdcopy.StdCopy(f, f, r)
				if err != nil {
					log.Printf("[%d] [FAILED_TO_COPY_LOGS] [%s] [Failed to copy data to log file %s: %v]", requestId, browserContainerId, filename, err)
				}
			}
*/
		},
	}

	log.Printf("[%d] [TASK_SERVICE_DETAILS] [%s]", requestId, s)
	return &s, nil
}

func GetTaskInfo(instanceID string, taskID string) {
	svc := ecs.New(awsSession.New(&aws.Config{Region: aws.String("us-east-1"), MaxRetries: aws.Int(10)}))
    input := &ecs.ListTasksInput{
        Cluster: aws.String("executor-cluster"),
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

func getEcsMemory(caps session.Caps) (int64, int64) {
        capsMemory := "768"
        if caps.Memory != "" {
                capsMemory = caps.Memory
        }
//        hardMemory, err := strconv.Atoi(capsMemory)
	hardMemory, err := strconv.ParseInt(capsMemory, 10, 64)
        if err != nil {
            fmt.Println(capsMemory, "is not an integer.")
        }

        capsMemoryReservation := "768"
        if caps.MemoryReservation != "" {
                capsMemoryReservation = caps.MemoryReservation
        }

//	softMemory, err := strconv.Atoi(capsMemoryReservation)
        softMemory, err := strconv.ParseInt(capsMemoryReservation, 10, 64)
	if err != nil {
	    fmt.Println(capsMemoryReservation, "is not an integer.")
	}

        return int64(hardMemory), int64(softMemory)
//        return int64(capsMemory), int64(capsMemoryReservation)
}

func getEcsCpu(caps session.Caps) (int64) {
	capsCpu := "512"
        if caps.Cpu != "" {
                capsCpu = caps.Cpu
        }

        cpu, err := strconv.Atoi(capsCpu)
        if err != nil {
            fmt.Println(capsCpu, "is not an integer.")
        }

        return int64(cpu)
}

func getEcsImage(caps session.Caps) string {
	// selenoid/[vnc_][browsername]:[version]
	vnc := ""
        if caps.VNC {
		vnc = "vnc_"
	}
	//TODO: don't forgent to insert ":" as prefix if version is not empty!
	version := ""
        if caps.Version != "" {
                version = ":" + caps.Version
        }

	//TODO: think about possibility to override using custom capabilities
	return fmt.Sprintf("selenoid/%s%s%s", vnc, caps.Name, version)
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

func removeTask(ctx context.Context, requestId uint64, taskArn string) {
        log.Printf("[%d] [REMOVING_TASK] [%s]", requestId, taskArn)

        //TODO: parametrize region
        svc := ecs.New(awsSession.New(&aws.Config{Region: aws.String("us-east-1")}))

        stopTaskInput := &ecs.StopTaskInput{
          Cluster: aws.String("executor-cluster"),
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

