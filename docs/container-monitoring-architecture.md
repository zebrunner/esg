# Container monitoring for ESG tasks

Status: proposal. Code changes are in two repositories only: `recorder` and `esg`. Container logs come from the existing CloudWatch log group. The only infrastructure change is one read permission for the existing task role.

## Goal

Give container-level data for Selenium, Playwright, and Generic tasks:
- the memory, CPU, and process count of each container
- how each container stopped, and the issues
- the logs of all containers

The overhead in the task must stay very low.

The work has two phases:

| Phase | Result |
|---|---|
| 1. Task debugging | Full data for one task, live and after the stop, found by its ID |
| 2. Monitoring dashboard | All running tasks on one page, history across tasks, filters by issue, no ID necessary |

Phase 1 has the data model, the IDs, and the extension points that phase 2 uses. Phase 2 adds code and does not change the phase 1 formats.

## Facts that shape the design

- The ECS Task Metadata Endpoint v4 (`/task`, `/task/stats`) gives the task ARN. For each container, it gives the limits, the status, the exit code, and the Docker stats. Only containers inside the task can read it. It gives no `stopCode` and no `OutOfMemoryError` text.
- The recorder runs in all three task types. It has an HTTP server on port 9080 that ESG already calls.
- The task role is shared with the user code in `browser` and `executor`. Thus, no ECS or CloudWatch read permission goes to the task role.
- When `AWS_LOGS_GROUP` is set, ECS sends the stdout of every container to CloudWatch. These are the logs in the ECS console. The stream name is `<generic|linux|playwright>/<container>/<ecsTaskId>` (`environment/environment.go:154-165`).
- In a normal stop, `browser` exits with code 143. This is not a failure.
- The uploader copies every file in the artifact folder to S3.

## IDs

Each record has both IDs:

| Field | Value | Source in the recorder |
|---|---|---|
| `esgId` | ESG router UUID. For Selenium and Playwright, the session ID that the client gets. For Generic, the launch UUID. | `ROUTER_UUID` environment variable |
| `ecsTaskId` | ECS task ID, for example `0123456789abcdef0123456789abcdef` | Last part of `TaskARN` from TMDE `/task` |
| `ecsTaskArn` | Full task ARN | TMDE `/task` |

ESG also adds the tag `esg-id=<router uuid>` to each task at `RunTask` (`service/ecs.go:81`, `starter/taskRegistrar.go:89`). Thus, the ECS console shows the ESG ID, and each ID leads to the other.

## S3 locations

The task type sets the S3 folder. The folder name is the ESG ID.

| Task type | Folder | Existing files | New files |
|---|---|---|---|
| Selenium, Playwright | `<workspace>/artifacts/test-sessions/<esgId>/` (`environment/linuxBrowser.go:193`) | `session.log`, `video.mp4` | `stats.json`, `containers.log` |
| Generic | `<workspace>/artifacts/launches/<esgId>/` (`environment/generic.go:291`) | `console.log` | `stats.json`, `containers.log` |

ESG finds the files with no search. It builds the key from the workspace (the basic-auth user), the route (session or launch), and the ESG ID. The existing `/logs` and `/tasks/:task/log` handlers use the same method (`handlers/tasks.go:352-412`).

## Phase 1: task debugging

### Recorder

1. **Sampler.** Every `STATS_INTERVAL` seconds (default 30, 0 turns it off), read `/task` and `/task/stats`. Append one line to `LOG_DIR/.stats-samples.ndjson`. Keep only last and peak values in memory.
2. **Summary for each container.** Status, exit code, limits, last and peak memory working set, CPU percent, PIDs, `unhealthySeen`, `stoppedWhileTaskRunning`.
3. **Stop cause.**

   | Signal | `stopCause` |
   |---|---|
   | `DELETE /finish?reason=<r>` | `esg_finish` with the ESG reason |
   | Exit file | `executor_finished` |
   | SIGTERM after the essential container stopped | `essential_exited` with its exit code |
   | SIGTERM in other cases | `external_stop` |

4. **Issues.**
   - `oom_kill_likely`: exit 137 and memory ≥ 95% of the limit.
   - `memory_pressure`: peak ≥ 90% of the limit.
   - Also `killed`, `essential_exit_nonzero`, `sidecar_stopped`, `init_failed`, `unhealthy`, and `cpu_over_reservation`.
5. **Final write.** At stop, before `Share()`:
   - Write `stats.json`: the summary, the stop cause, the issues, and all samples.
   - Read the container logs from CloudWatch and write `containers.log` (see "Container logs").
   - The uploader copies both files to the S3 folder of the task type. After a Playwright rotate, the recorder publishes them as a separate artifact for `ROUTER_UUID`, so the folder is always the ESG ID folder.
6. **Current write.** `GET /stats` returns the summary and the issues now, without the samples. `GET /logs` reads the container logs from CloudWatch now.

New settings: `STATS_INTERVAL` and `ESSENTIAL_CONTAINER`. Release as recorder 2.5.0.

### Record format (shared by phase 1 and phase 2)

The same summary object goes into `stats.json`, `GET /stats`, and the phase 2 report. Only `samples` is in the file only.

```json
{
  "version": 1,
  "esgId": "11111111-2222-3333-4444-555555555555",
  "ecsTaskId": "0123456789abcdef0123456789abcdef",
  "ecsTaskArn": "arn:aws:ecs:<region>:<account>:task/<cluster>/0123456789abcdef0123456789abcdef",
  "envType": "linux",
  "family": "<env>-linux-chrome-latest",
  "state": "stopped",
  "startedAt": "…", "updatedAt": "…", "stoppedAt": "…",
  "stopCause": "essential_exited",
  "stopDetail": "browser exit code 137",
  "issues": [{ "code": "oom_kill_likely", "container": "browser", "severity": "error", "detail": "exit 137, 99% of 1024 MiB" }],
  "containers": [{
    "name": "browser", "essential": true, "lastStatus": "STOPPED", "exitCode": 137,
    "limits": { "cpu": 1024, "memoryMiB": 1024 },
    "memory": { "lastBytes": 1062000000, "peakBytes": 1071000000, "peakAt": "…" },
    "cpu": { "lastPct": 12.0, "peakPct": 180.2 }, "pids": { "peak": 141 }
  }],
  "samples": [{ "t": "…", "seg": "…", "c": { "browser": [690000000, 87.4, 64] } }]
}
```

- `state` is `running` in `GET /stats` and `stopped` in `stats.json`.
- The first line of `.stats-samples.ndjson` also has `esgId` and `ecsTaskId`.
- A new field never changes the meaning of an old field. A change that breaks the format increases `version`.

### Container logs

The recorder reads the same logs as the ECS console from CloudWatch at task end. No container writes extra files and no wrapper is necessary.

1. ESG gives the recorder `AWS_LOGS_GROUP` and `LOGS_STREAM_PREFIX` (`generic`, `linux`, or `playwright`).
2. At stop, the recorder waits about 5s, so `awslogs` can send the last lines.
3. For each container from TMDE `/task`, it reads the stream `<prefix>/<container>/<ecsTaskId>` with `GetLogEvents`. A container with no output has no stream and is skipped.
4. It merges the lines by time into `LOG_DIR/containers.log`: `<time> [<container>] <line>`. The first line has `esgId` and `ecsTaskId`. `CONTAINER_LOG_MAX_MIB` (default 20) limits each container.
5. `Share()` moves the file into the artifact folder. The uploader copies it to S3 with `stats.json`.

The recorder uses the AWS SDK with the task role credentials. ECS gives them to all containers of the task.

Requirements:
- `AWS_LOGS_GROUP` is set (terraform `enable_cloudwatch = true`). If it is not set, the recorder writes no `containers.log`. `stats.json` still works.
- The existing task role gets `logs:GetLogEvents` on the task log group (`e3s-terraform-deploy/deploy/iam_data/task-policy.json`).
- If the CloudWatch VPC endpoint is used, its policy allows `logs:GetLogEvents` (`iam_data/cloudwatch-endpoint-policy.json`). Its security group already allows the agent instances.

Security condition: IAM grants `logs:GetLogEvents` for the full log group, not for one stream. The task role is shared with the user code in `browser` and `executor`. Thus, user code in any task can read the logs of all tasks in the log group. Use this only when one deployment serves one customer.

### Router

1. Recorder environment: add `ESSENTIAL_CONTAINER` (`browser` or `executor`).
2. Add `?reason=` to the existing `DELETE /finish` call (`selenium/recorder.go:118-140`).
3. Add the `esg-id` tag at `RunTask`.
4. Add the network endpoints `recorderStats` (`/stats`) and `recorderLogs` (`/logs`) on port 9080.
5. Add `AWS_LOGS_GROUP` and `LOGS_STREAM_PREFIX` to the recorder environment.
6. New routes, next to `/logs/:session` and `/tasks/:task/log`:

   | Route | Running task | Stopped task |
   |---|---|---|
   | `GET /stats/:session` | Proxy to recorder `/stats` | Redirect to `test-sessions/<esgId>/stats.json` |
   | `GET /containers-log/:session` | Proxy to recorder `/logs` | Redirect to `test-sessions/<esgId>/containers.log` |
   | `GET /tasks/:task/stats` | Proxy to recorder `/stats` | Redirect to `launches/<esgId>/stats.json` |
   | `GET /tasks/:task/containers-log` | Proxy to recorder `/logs` | Redirect to `launches/<esgId>/containers.log` |

7. Generic tasks have no IP in the mapper. To proxy, the router gets the host port and the instance IP with `DescribeTasks`, `DescribeContainerInstances`, and `DescribeInstances`. The server role already allows these calls.
8. Put the new handlers in a `monitoring` package. Phase 2 adds to it.
9. Change the default recorder tag to 2.5 (`environment/builder.go:19`).

The scaler, Redis, Postgres, and all other images (uploader, mitm, clone, entrypoint, maven) do not change.

### Where the user gets the ESG ID

- The Zebrunner reporting portal: the session of each test.
- The test output: the Selenium or Playwright session ID, or the Generic launch UUID.
- The ECS console: the `esg-id` tag.
- `stats.json` gives the ECS task ID back.

### Extension points for phase 2

| Phase 1 item | Use in phase 2 |
|---|---|
| One record format with `esgId`, `ecsTaskId`, `envType`, `state` | The report row and the dashboard row are the same object |
| `GET /stats` on each recorder | The live fleet view calls it for all running tasks |
| Recorder output through a sink interface (file sink in phase 1) | Phase 2 adds an HTTP sink that sends the final summary to ESG |
| `monitoring` package in the router | Phase 2 adds the list, report, and page handlers |
| `esg-id` tag | The dashboard links ECS tasks and ESG IDs |

## Phase 2: monitoring dashboard

| Item | How | Cost |
|---|---|---|
| Live fleet view | `GET /api/monitoring/running`: the router calls `GET /stats` on all running tasks (from Redis) in parallel, with a worker pool and a 2s timeout | Work only on request |
| Report from the recorder | At stop, the HTTP sink sends the summary (no samples) to `POST /internal/monitoring/report`. ESG gives each recorder a token in its environment. | One request of about 1 KB for each task |
| History | The router stores one row for each task in its Postgres: IDs, workspace, task type, times, stop cause, issue codes, peaks for each container. Migration `000003`. | About 1 KB for each task. Retention is a setting. |
| Queries | `GET /api/monitoring/tasks?workspace=&envType=&issue=&since=&until=` | One SQL query |
| Dashboard page | A static HTML page in the router over these endpoints. Each row links to `/stats` and `/containers-log`. | No runtime cost |

Phase 2 does not change the recorder file format or the phase 1 routes.

## Overhead

| Place | Cost |
|---|---|
| Recorder | Two local TMDE reads and one append of about 400 B every 30s. Constant memory. About 2s more at stop for the files. |
| Recorder image | The AWS SDK for CloudWatch Logs adds 6.0 MB to the binary (32.7 MB to 38.7 MB, measured with the current build flags) and 1.3 MB to the compressed download. `-ldflags="-s -w"` in the Dockerfile removes about 10 MB of debug information. |
| Other containers | No change |
| Task volume and S3 | About 45 KB of samples for each hour |
| CloudWatch | No change to the writes. One read of each container stream at task end, and reads on request for a running task. |
| Router | Work only on request in phase 1 |

## Test before deployment

### 1. Local Docker setup

Build a Docker Compose setup in `recorder/test/` that is the same as one ESG test run:
- **Generic task:** `clone`, `entrypoint`, `executor`, `recorder`, `uploader`. The `executor` runs a test project that starts 4 browser sessions at the same time.
- **4 browser tasks:** each is `browser`, `recorder`, `uploader`. The executor sends its sessions to the 4 Selenium ports directly.
- **Same limits as ECS:** recorder 64 MiB in the Generic task and 1024 MiB in the browser tasks. Browser and executor at least 1024 MiB.
- **TMDE mock:** a small server that serves `/task` and `/task/stats` from the Docker Engine API (read-only socket) for each group. `ECS_CONTAINER_METADATA_URI_V4` in each recorder points to it.
- **Uploader:** a dev S3 bucket, or turn off the upload for the local run.

Measure, with `STATS_INTERVAL` set to 0, 60, 30, and 15:
- the memory and CPU of each recorder from `docker stats` (overhead compared with 0)
- the peak memory of each container and of the full setup
- the size of `.stats-samples.ndjson`, `stats.json`, and `containers.log`
- the recorder memory with the AWS SDK during the log read at stop
- the time from SIGTERM to the end of `Share()`

Check that `esgId` and `ecsTaskId` are correct in the files and in `GET /stats`. Compare the values with `docker stats`. Force an OOM in one browser with a memory-heavy page. Confirm `oom_kill_likely`.

### 2. Dev ECS cluster

1. Deploy the new recorder and ESG.
2. Start the same Generic test run through ESG with 4 parallel browser sessions.
3. Confirm the files in `launches/<esgId>/` for the Generic task and in `test-sessions/<esgId>/` for each browser task.
4. Confirm the live routes while the tasks run, and the `esg-id` tag in the ECS console. Compare `/containers-log` with the logs in the ECS console.
5. Repeat the measurement in step 1 from the recorder samples.

### 3. Result

Make a table with the memory (average and peak) and the CPU of each container for each task type. Show the recorder overhead at each interval. Use the table to select the default `STATS_INTERVAL` and to check the container memory limits.

## Limitations

- The OOM kill is an inference (exit 137 with memory near the limit). ECS shows the exact reason for about one hour only.
- An OOM kill of one browser process (a crashed tab) is not a container exit. Only `memory_pressure` and the samples show it.
- Host `/dev/shm` is shared by all browser tasks on an instance. Per-task stats cannot show its usage.
- If the recorder is killed, the task has no `stats.json` and no `containers.log`. The logs are still in CloudWatch for the retention time of the log group (3 days today).
- The recorder stops before some other containers. Their last lines after the recorder read are not in `containers.log`.
- The task role can read all logs in the log group (see the security condition in "Container logs").
- Phase 1 shows one task at a time. Phase 2 removes this limit.
- Windows and Cypress tasks are not in scope.

## Open decisions

1. Default `STATS_INTERVAL`, after the test.
2. Route groups: `httpHub` and `/api` as the existing artifact routes, or `/api` only.
3. Log size limit: 20 MiB for each container, or a different value.
4. Phase 2 history retention.
5. Approve unit tests for the recorder (TMDE parsing, sampler, stop cause, issues, file writer, log merge) and for the router (key building, proxy).
