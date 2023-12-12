## Capabilities

Capabilities could be passed one by one as map{string:any} with prefix `zebrunner:` in key. Example - `"zebrunner:enableDebug":true`.

Or as map{string:map{string:any}}, where the key in the first map should be `zebrunner:options`, and simple keys in the second. Example -  `map{"zebrunner:options": map{"enableVNC":true, "enableVideo":false, "mitm":"false"}}`.

If the same capability but with different values were passed by prefix and map options, value usage priority will be given to the capability with prefix.

### Supported list

* `enableDebug` - Default: false. Value type: bool/string. Enables extended error response in case of service startup failure.
* `idleTimeout` - Default: 60(seconds = 1 min). Value type: integer/string (in seconds). Session timeout without any interaction before shutdown. Max value - 1200 (20 min).
* `maxTimeout` - Default: 86400(seconds = 24 hours). Value type: integer/string (in seconds). Maximum session duration in seconds.

#### Selenium linux browser capabilities:

* `enableVNC` - Default: true. Value type: bool/string. Enables vnc for session.
* `enableVideo` - Default: true. Value type: bool/string. Enables video recording.
* `screenResolution` - Default: 1920x1080x24. Value type: string. Determines session screen resolution. Could be passed only as full or short resolution format. Min screen resolution is 40x30. Max aspect ratio is 1:6 or 6:1.
* `videoScreenSize` - Default: value from screenResolution. Value type: string. Determines recording screen resolution. Cannot be higher than actual screen resolution. Max total pixels: 3_000_000 (example: 1920x1080=2_073_600).
* `frameRate` - Default: 12. Value type: integer/string. Determines video recorder fps. Value could be set between 1 and 30 fps.
* `hostEntries` - Default: -. Value type: string array. Selenoid's [hostEntries](https://aerokube.com/selenoid/latest/#_hosts_entries_hostsentries) capability.
* `dnsServers` - Default: -. Value type: string array. Selenoid's [dnsServers](https://aerokube.com/selenoid/latest/#_custom_dns_servers_dnsservers) capability.
* `timeZone` - Default: utc 0. Value type: string. Specifies a particular time zone in operating system for the session. Example: Asia/Kolkata.
* `mitm` (alias: `Mitm`) - Default: false. Value type: bool/string. Enables mitm proxy. Usage tracker includes allocated resources for mitm container.

#### Selenium windows browser capabilities

* `screenResolution` - Default: 1920x1080x24. Value type: string. Determines session screen resolution. Could be passed only as full or short resolution format. Min screen resolution is 40x30. Max aspect ratio is 1:6 or 6:1.

### Browsers resource allocation

* `cpu` (alias: `Cpu`) - Value type: integer/string. CPU limitation for executor container measured in [aws units](https://repost.aws/knowledge-center/ecs-cpu-allocation).
* `memory` (alias: `Memory`) - Value type: integer/string. Memory (RAM) limitation for executor container measured in MiB.

> Max limitation for cpu/memory is configurable uniquely for every ESG server. Max values by default: cpu: 16384, memory: 28675.

| Environment            | Min (cpu, memory) | Default (cpu, memory) |
| ---------------------- | ----------------- | --------------------- |
| Emulators (appium)     | 2048, 2048        | 2048, 2048            |
| Cypress                | 1024, 2048        | 1024, 2048            |
| Linux browser          | 1024, 1024        | 1024, 1024            |
| Windows browser        | 1024, 1024        | 1024, 1024            |

> Max limitation for mitmCpu/mitmMemory is configurable uniquely for every ESG server. Max values by default are also: cpu: 16384, memory: 28675.
* `mitmCpu` (alias: `MitmCpu`, `mitmcpu`) - Min: 512. Default: 512.Value type: integer/string. CPU limitation for mitm container measured in [aws units](https://repost.aws/knowledge-center/ecs-cpu-allocation). 
* `mitmMemory` (alias: `MitmMemory`, `mitmmemory`) - Min: 512. Default: 512. Value type: integer/string. Memory (RAM) limitation for mitm container measured in MiB.
