# ESG

ESG is a ideological heir of [Selenoid](https://github.com/aerokube/selenoid) hub using [Docker](https://docker.com/) containers to launch browsers using AWS scaling capabilities.

## Features

### Ready to use Browser Images
No need to manually install browsers or dive into WebDriver documentation.
New images are added right after official releases.

### Video Recording
* Any browser session can be saved to [H.264](https://en.wikipedia.org/wiki/H.264/MPEG-4_AVC) video ([example](https://www.youtube.com/watch?v=maB298oO5cI))
* An API to list, download and delete recorded video files

### Convenient Logging

* Any browser session logs are automatically saved to files - one per session
* An API to list, download and delete saved log files

### Lightweight and Lightning Fast
Suitable for personal usage and in big clusters:
* Consumes **10 times** less memory than Java-based Selenium server under the same load
* **Small 6 Mb binary** with no external dependencies (no need to install Java)
* **Browser consumption API** working out of the box
* Fully **isolated** and **reproducible** environment

### Documentation

#### Infrastructure Setup
* [Infrastructure Deploy with Terraform](https://github.com/zebrunner/e3s-terraform-deploy) - Automated infrastructure deployment using Terraform
* [Manual Infrastructure Setup Guide](https://github.com/zebrunner/e3s/blob/docs/docs/e3s-manual-infra-creation/ESG%20Deploy%20step%20by%20step%20guide.md) - Step-by-step guide for manual AWS infrastructure creation

#### Usage
* [Usage Guide](https://github.com/zebrunner/e3s/blob/main/docs/usage.md)
