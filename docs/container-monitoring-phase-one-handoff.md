# Container monitoring phase one handoff

Updated: October 16, 2026.

The basic phase one implementation is ready for further testing in ESG and recorder. It is not ready for deployment. Continue with Docker integration tests and resource measurements before release.

The [architecture proposal](container-monitoring-architecture.md) defines the intended behavior and the full validation plan. Phase two has not started.

## Required workspace

**The recorder repository must be open in the same workspace as ESG to continue this work.** ESG does not contain the recorder source. Changes and tests require both repositories.

| Workspace folder | Repository | Branch |
|---|---|---|
| `esg` | [zebrunner/esg](https://github.com/zebrunner/esg) | `feature-container-monitoring` |
| `recorder` | [zebrunner/esg-component-recorder](https://github.com/zebrunner/esg-component-recorder) | `feature-container-monitoring` |

The current local setup uses sibling `esg` and `recorder` folders. Select the feature branch in each repository before further work.

Use Docker for local runtime tests. Do not use AWS CLI or call live AWS services for local validation. Local mocks can serve task metadata, task role credentials, and CloudWatch Logs responses.

## Implemented behavior

ESG adds these routes in its regular HTTP group and under `/api`:

| Route | Live source | Final artifact |
|---|---|---|
| `GET /stats/:session` | Recorder `/stats` | `test-sessions/<esgId>/stats.json` |
| `GET /containers-log/:session` | Recorder `/logs` | `test-sessions/<esgId>/containers.log` |
| `GET /tasks/:task/stats` | Recorder `/stats` | `launches/<esgId>/stats.json` |
| `GET /tasks/:task/containers-log` | Recorder `/logs` | `launches/<esgId>/containers.log` |

Final artifacts use the existing `<workspace>/artifacts/` prefix. Regular routes authenticate the workspace user and check ownership before a live proxy request. The `/api` routes use existing administrator authentication. If the cache entry has expired, administrator requests require `?workspace=<workspace>`.

Child session IDs resolve to the original ESG ID while their cache pointers exist. After mapper expiry, use the original ESG ID for monitoring artifacts.

ESG also sets the recorder metadata, log settings, and essential container name. It adds the `esg-id` task tag and passes a reason to `/finish`. Generic tasks resolve the recorder address through the AWS SDK. The default recorder image tag is now `2.5`.

Recorder samples the task metadata endpoint and keeps last and peak resource values in memory. It appends samples to `.stats-samples.ndjson`. The live `/stats` response contains the summary and issues without samples. At shutdown, `stats.json` contains the summary and samples in record format version `1`.

Recorder detects stop causes and resource issues. It excludes normal browser exit code `143` from nonzero exit issues. It treats `clone`, `entrypoint`, and `maven` as initialization containers when it classifies issues.

When logs are enabled, recorder reads CloudWatch Logs through the AWS SDK with task role credentials. It merges events by timestamp and limits output for each container. A log read failure does not prevent recorder from writing the stats artifact.

After a Playwright rotation, task monitoring artifacts retain the original `ROUTER_UUID` folder. Samples identify the active artifact segment. Publication waits up to 10 seconds for an earlier upload of the original folder to finish. Pending or failed upload markers can prevent publication and require further validation.

| Setting | Current default or value |
|---|---|
| `STATS_INTERVAL` | `30` seconds. `0` disables periodic resource samples. |
| `CONTAINER_LOG_MAX_MIB` | `20` MiB for each container |
| `ESSENTIAL_CONTAINER` | ESG sets `browser` or `executor`. |
| `ENV_TYPE` | ESG sets `linux`, `playwright`, or `generic`. |
| `AWS_LOGS_GROUP` | ESG supplies the configured group. Empty disables container log collection. |
| `LOGS_STREAM_PREFIX` | ESG sets the task type. |

The recorder build script tags version `2.5.0` and alias `2.5`. No image release or deployment has occurred.

## Source locations

| Repository | Files |
|---|---|
| ESG | `monitoring/handlers.go`, `monitoring/network.go`, `cmd/router/main.go` |
| ESG | `environment/{builder,linuxBrowser,playwrightBrowser,generic}.go` |
| ESG | `selenium/{recorder,session}.go`, `service/ecs.go`, `starter/taskRegistrar.go` |
| Recorder | `server/monitoring/` for the record format, sampler, issues, file sink, and log reader |
| Recorder | `server/handlers/monitoring.go`, `server/service/monitoring.go` |
| Recorder | `server/cmd/main.go`, `server/config/config.go`, existing handlers and artifact sharing |

## Validation completed

Both final source checkouts compile in Docker. Temporary smoke checks passed with local mocks and container networking disabled:

- Recorder live stats, memory working set, CPU percentage, process count, and final artifacts.
- Explicit finish, essential exit, external stop, and Generic exit file causes.
- OOM inference from simulated metadata and normal browser exit code `143`.
- Disabled sampling and disabled logs.
- Paginated log reads, timestamp ordering, absent streams, size limits, and log read failures.
- Playwright rotation and final monitoring artifacts under the original ESG ID.
- Router workspace checks, child session IDs, Generic address resolution, final artifact keys, stale stopped tasks, and cache failures.

These checks used temporary harnesses outside the repositories. The repositories do not yet contain permanent tests or a Docker Compose test setup for this feature. Live ECS, S3, and CloudWatch validation remains. The OOM check used simulated exit and memory data.

To repeat the compile checks, run each command from the specified repository root. Docker may download Go modules.

From ESG:

```sh
docker run --rm --platform=linux/amd64 -v "$PWD":/src:ro -w /src golang:1-alpine go build -o /tmp/router ./cmd/router
```

From recorder:

```sh
docker run --rm --platform=linux/amd64 -v "$PWD/server":/src:ro -w /src golang:1-alpine go build -o /tmp/recorder ./cmd
```

## Next steps

1. Create the reproducible Docker setup described in the architecture proposal, with one Generic task and four parallel browser tasks. Disable uploads locally.
2. Connect the metadata mock to the Docker Engine API. Compare recorder measurements with `docker stats`.
3. Measure recorder overhead at intervals `0`, `60`, `30`, and `15` seconds. Check the Generic recorder limit of `64` MiB.
4. Measure log read memory, artifact sizes, and the time from SIGTERM through artifact publication.
5. Force a real browser container OOM and verify the recorded issue and exit details.
6. Test rotation with an active uploader, delayed uploads, failed uploads, and shutdown timeouts. Verify original and child artifact folders.
7. Obtain approval before adding permanent unit tests, as the repository instructions require. Prioritize metadata parsing, calculations, stop causes, issues, log pagination, file writing, and router authorization.
8. Select the sampling interval and log limit from the measurements. Keep `30` seconds and `20` MiB provisional until then.

Before deployment, verify the documented `logs:GetLogEvents` permission and CloudWatch endpoint policy. The shared task role can read other tasks' logs in the same group. Follow the architecture proposal's condition that one deployment serves one customer. This work includes no IAM or endpoint policy changes.

After local validation, perform the separate dev ECS checks from the architecture proposal. Windows, Cypress, and phase two remain outside this implementation.
