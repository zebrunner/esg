<powershell>
    Import-Module ECSTools
    [Environment]::SetEnvironmentVariable("ECS_LOGLEVEL", "info", "Machine")
    [Environment]::SetEnvironmentVariable("ECS_LOGLEVEL_ON_INSTANCE", "info", "Machine")
    [Environment]::SetEnvironmentVariable("ECS_CONTAINER_STOP_TIMEOUT", "15s", "Machine")
    [Environment]::SetEnvironmentVariable("ECS_ENGINE_TASK_CLEANUP_WAIT_DURATION", "5m", "Machine")
    [Environment]::SetEnvironmentVariable("ECS_ENABLE_SPOT_INSTANCE_DRAINING", $TRUE, "Machine")
    [Environment]::SetEnvironmentVariable("ECS_PULL_DEPENDENT_CONTAINERS_UPFRONT", $TRUE, "Machine")
    [Environment]::SetEnvironmentVariable("ECS_ENABLE_CONTAINER_METADATA", "$TRUE", "Machine")
    [Environment]::SetEnvironmentVariable("ECS_IMAGE_PULL_BEHAVIOR", "prefer-cached", "Machine")
    
    Initialize-ECSAgent -Cluster '${cluster_name}' -EnableTaskIAMRole -EnableTaskENI -AwsvpcBlockIMDS -LoggingDrivers '["json-file","awslogs"]' -AwsvpcAdditionalLocalRoutes '["${cidr_block}"]'

    Set-TimeZone -Name "Pacific Standard Time"
</powershell>
<persist>true</persist>
