# AWSVPC Network Mode Specification - E3S Version: 3.1.8+

## Overview

This document describes the AWSVPC network mode implementation in E3S and the key differences from the previous bridge network mode.

## Network Mode Comparison

### Bridge Mode (Previous)
- **Host Port Mapping**: Container ports mapped to dynamic host ports (HostPort: 0)
- **IP Address**: Used EC2 instance public/private IP
- **Container Links**: Required Docker links between containers
- **Port Discovery**: Dynamic port allocation required runtime discovery
- **Networking**: Containers share host network namespace via bridge

### AWSVPC Mode (Current)
- **Host Port Mapping**: Container port equals host port (HostPort: containerPort)
- **IP Address**: Each task receives its own ENI (Elastic Network Interface) with private IP
- **Container Links**: Not supported; containers communicate via localhost or task IP
- **Port Discovery**: Static port allocation, no runtime discovery needed
- **Networking**: Each task has its own network namespace with dedicated ENI

## Key Implementation Changes

### 1. Port Mapping Configuration

In AWSVPC mode, container ports are directly mapped to host ports instead of using dynamic allocation.

**Example from `environment/generic.go` (lines 361-366):**

```go
Endpoints: map[string]*network.Endpoint{
    "driver":         {ContainerPort: genericPort, HostPort: genericPort, Path: "/"},
    "recorderStart":  {ContainerPort: recorderdPort, HostPort: recorderdPort, Path: "/start"},
    "recorderStop":   {ContainerPort: recorderdPort, HostPort: recorderdPort, Path: "/stop"},
    "recorderFinish": {ContainerPort: recorderdPort, HostPort: recorderdPort, Path: "/finish"},
}
```

**Previously (Bridge Mode):**
```go
Endpoints: map[string]*network.Endpoint{
    "driver":         {ContainerPort: genericPort, HostPort: 0, Path: "/"},
    "recorderStart":  {ContainerPort: recorderdPort, HostPort: 0, Path: "/start"},
    // ...
}
```

### 2. Task IP Usage

**Critical Difference**: In AWSVPC mode, the **task's private IP address** is used for communication, not the host EC2 instance IP.

- Each ECS task receives its own ENI with a private IPv4 address from the specified subnet
- Containers within the task communicate via `localhost`
- External communication uses the task's private IP (extracted from ENI attachment metadata)
- For ELB target registration, the task IP is registered, not the instance IP

**Implementation**: See `utils/tasks.go` - `GetAwsVpcTaskPrivateIPv4()` function

### 3. Required Configuration

#### Security Groups and Subnets

**Configuration Properties** (`properties/router.env`):
```bash
# AWSVPC settings
# Format: SECURITY_GROUPS=sg-1,sg-2 and SUBNET=subnet-1
SECURITY_GROUPS=sg-xxxxx,sg-yyyyy
SUBNET=subnet-xxxxx
```

These are passed to ECS task registration:

```go
NetworkConfiguration: &ecs.NetworkConfiguration{
    AwsvpcConfiguration: &ecs.AwsVpcConfiguration{
        SecurityGroups: config.Conf.SecurityGroups.ToStringSlice(),
        Subnets:        config.Conf.Subnet.ToStringSlice(),
    },
}
```

## Critical Limitations and Requirements

### ⚠️ Single Subnet Requirement for Auto Scaling Groups

**IMPORTANT**: When using Auto Scaling Groups (ASG) with AWSVPC mode, you **MUST configure only ONE subnet** in your ASG configuration.

#### Why This Limitation Exists

AWS ECS task placement and EC2 Auto Scaling operate independently, which can cause subnet mismatches:

1. **Task Registration**: When a task is registered via `RunTask`, ECS selects a subnet from the `NetworkConfiguration.AwsvpcConfiguration.Subnets` list
2. **Instance Launch**: AWS Auto Scaling decides which subnet to launch the EC2 instance based on ASG configuration
3. **Mismatch Risk**: If ASG has multiple subnets, it may launch an instance in Subnet-A while ECS registered the task's ENI in Subnet-B
4. **Result**: Task cannot start because the ENI is in a different subnet than the instance

#### Solution

Configure your Auto Scaling Group with a **single subnet** that matches the subnet specified in your E3S AWSVPC configuration:

```bash
# E3S Configuration
SUBNET=subnet-xxxxx

# AWS ASG Configuration (via AWS Console/CLI/Terraform)
# Set only ONE subnet: subnet-xxxxx
```

This ensures that:
- ECS tasks always get ENIs in the specified subnet
- ASG always launches instances in the same subnet
- No subnet mismatch can occur

### Security Group Requirements

Security groups must allow:
- **Inbound**: Specific ports from E3S Router/Server IP (see table below)
- **Outbound**: Internet access (for pulling Docker images, if using NAT Gateway)
- **Outbound**: Access to AWS services (ECR, S3, CloudWatch Logs, etc.)

#### Minimal Required Inbound Ports

**Key Advantage**: AWSVPC mode requires only specific ports instead of dynamic port ranges (previously 32472-64567 in bridge mode).

**Example AWS Security Group Configuration:**
```
Inbound Rules:
------------------------------------------------------------------------------------------
Name      Rule ID        IP Version Type     Protocol   Port Range Source
--------- -------------- ---------- -------- ---------- ---------- ----------------------
rule-1    sgr-xxxxxx2    IPv4       All TCP  TCP        4444       <ESG_SERVER_IP/32>
rule-2    sgr-xxxxxx3    IPv4       All TCP  TCP        4723       <ESG_SERVER_IP/32>
rule-3    sgr-xxxxxx4    IPv4       All TCP  TCP        5900       <ESG_SERVER_IP/32>
rule-4    sgr-xxxxxx5    IPv4       All TCP  TCP        7070       <ESG_SERVER_IP/32>
rule-5    sgr-xxxxxx6    IPv4       All TCP  TCP        8060       <ESG_SERVER_IP/32>
rule-6    sgr-xxxxxx7    IPv4       All TCP  TCP        8080       <ESG_SERVER_IP/32>
rule-7    sgr-xxxxxx8    IPv4       All TCP  TCP        8081       <ESG_SERVER_IP/32>
rule-8    sgr-xxxxxx9    IPv4       All TCP  TCP        8082       <ESG_SERVER_IP/32>
rule-9    sgr-xxxxxx10   IPv4       All TCP  TCP        9080       <ESG_SERVER_IP/32>
rule-10   sgr-xxxxxx11   IPv4       All TCP  TCP        9090       <ESG_SERVER_IP/32>

Outbound Rules:
- Type: All traffic, Destination: 0.0.0.0/0
```

**Notes:**
- Replace `<ESG_SERVER_IP/32>` with your E3S Router/Server private IP address
- For ALB/NLB integration, add the load balancer's security group as source for port 4444
- All ports use static mapping (no dynamic port allocation)

## Removed Features

### Docker Links
Container links are no longer supported or required in AWSVPC mode:

```go
// REMOVED from Container struct
Links: []string // List of linked containers
```

**Migration**: Containers within the same task can communicate via `localhost` on their respective ports.

### USE_PUBLIC_IP Flag
The `USE_PUBLIC_IP` configuration option has been removed as AWSVPC tasks use ENI private IPs by default.

## Proxy and MITM Configuration Changes

### Proxy Address Update

The proxy configuration has been changed to use `localhost` instead of container links:

**`capabilities/parsing.go`:**
```go
"proxy": map[string]interface{}{
    "httpProxy": "localhost:8081",
    "sslProxy":  "localhost:8081",
    "proxyType": "manual",
}
```

### MITM Proxy Port
A separate file server port has been added for MITM proxy:

```go
fileserverPort     int64 = 8080  // Browser file server
fileserverPortMitm int64 = 8082  // MITM file server, may not work, need to check
```

### Retry Logic

AWSVPC mode includes enhanced retry logic for transient network issues:

```go
retryTransport := &utils.RetryingTransport{
    Base:    http.DefaultTransport,
    Retries: 1,
    Delay:   500 * time.Millisecond,
}
```

This handles:
- Temporary connection failures
- DNS resolution delays
- Container startup timing issues

## Migration Checklist

When migrating from bridge to AWSVPC mode:

- [ ] Configure `SECURITY_GROUPS` with appropriate security groups
- [ ] Configure `SUBNET` with a single private subnet
- [ ] Ensure ASG uses the **exact same single subnet**
- [ ] Remove any `USE_PUBLIC_IP` configuration
- [ ] Verify NAT Gateway configuration for outbound internet access
