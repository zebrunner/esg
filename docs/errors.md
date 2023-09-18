If something went wrong on creation/execution/finish phase, client will recieve an error message from ESG or Selenium. This paragraph will describle all possible errors from ESG side.

> To enable extened error response by ESG, pass zebrunner:enableDebug=true capability.

### Selenium type errors:

* name: `session not created`, status: `500`.
    * `failed to start executor` - failed to build execution environment (browser/version/device is not supported).
    * `service startup timed out` - failed to start service under 9 mins (by defaut).
    * `error forwarding the new session request timed out waiting for a node to become available` - all nodes are busy, and task didn't make it to start in time (no free resources for task were found).
    * `failed to create task` - failed to find existing task defenition or to place a new task into pending task's pool.
    * `failed to start task` - failed to start healthy (executable) task due to wrong parameters/internal error.
    * `failed to set network configuration` - failed to find host port for newly created task due to any fatal internal error.
    * `failed to start driver` - usually the main reason is a wrong selenium's driver capabilities/driver capabilities format.
    * `service startup failed` - internal error connected with scaler/router/cluster.
    * `service start has been aborted` - task creationg has been aborted externally.


* name: `invalid argument`, status: `400`.
    * `failed to process capabilities` - some capabilities are wrong format/type.


* name `invalid session id`, status `404`.
    * `session timed out or not found` - session doesn't exist or cache was already flushed.


* name `session stopped`, status `403`.
    * `session stop reason` - session cannot be accessed anymore because it was finished.


* name `invalid task id`, status `404`.
    * `task timed out or not found` - task doesn't exist or cache was already flushed.


* name `task stopped`, status `403`.
    * `task stop reason` - task cannot be accessed anymore because it was finished.


* name `invalid credentials`, status `401`.
    * `credentials not provided` - request without credentials.
    * `invalid username or password` - invalid credentials.


* name: `unknown error`, status: `500`. Contains other ESG internal errors
