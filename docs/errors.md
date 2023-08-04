If something went wrong on creation/execution/finish phase, client will recieve an error message from ESG or Selenium. This paragraph will describle all possible errors from ESG side.

> To enable extened error response by ESG, pass zebrunner:enableDebug=true capability.

### Selenium type errors:

* name: `session not created`, status: `500`.
    1. `failed to start executor` - failed to build execution environment.
    2. `error forwarding the new session request timed out waiting for a node to become available` - all nodes are busy, and task didn't make it to start in time.
    3. `service startup timed out` - task was recreated several times because of a failure. As result couldn't wait until task is running and healthy.
    4. `failed to run task` - aws overload or wrong browserVersion/browserName/deviceName/platformName capability.
    5. `failed to start driver` - usually the main reason is a wrong selenium's driver capabilities.
    6. `driver startup timed out` - driver couldn't start in time


* name: `invalid argument`, status: `400`.
    1. `bad JSON format` - invalid json capabilities format
    2. `failed to process capabilities` - some capabilities are wrong format/type


* name `invalid session id`, status `404`.
    1. `session timed out or not found` - session doesn't exist or cache was already flushed.


* name `session stopped`, status `403`.
    1. `stop reason` - session cannot be accessed anymore because it was finished


* name `invalid task id`, status `404`.
    1. `task timed out or not found` - task doesn't exist or cache was already flushed.


* name `task stopped`, status `403`.
    1. `stop reason` - task cannot be accessed anymore because it was finished


* name `invalid credentials`, status `401`.
    1. `invalid username or password` - can't find/invalid credentials


* name: `unknown error`, status: `500`. Contains ESG internal errors
