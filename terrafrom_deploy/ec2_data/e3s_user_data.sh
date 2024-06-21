#!/bin/bash

user="ubuntu"
e3s-path="/home/${user}/tools/e3s"

sudo apt-get update && sudo apt-get upgrade

sudo mkdir -p ${e3s-path}

sudo git clone https://github.com/zebrunner/e3s.git ${e3s-path}

# Add Docker's official GPG key:
sudo apt-get -y install ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc

# Add the repository to Apt sources:
echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
  sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
sudo apt-get update

sudo apt-get -y install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# Add user to docker group
sudo usermod -a -G docker ${user}

# TODO: implement vars replacement instead of *.env files flushing
cd ${e3s-path}
# config.env
> ./properties/config.env
echo AWS_REGION=${region} >> ./properties/config.env
echo AWS_CLUSTER=${cluster_name} >> ./properties/config.env
echo AWS_TASK_ROLE=${task_role} >> ./properties/config.env
echo ZEBRUNNER_HOST=${zbr_host} >> ./properties/config.env
echo ZEBRUNNER_INTEGRATION_USER=${zbr_user} >> ./properties/config.env
echo ZEBRUNNER_INTEGRATION_PASSWORD=${zbr_pass} >> ./properties/config.env
echo ZEBRUNNER_ENV=${env} >> ./properties/config.env
echo LOG_LEVEL=info >> ./properties/config.env

# router.env
> ./properties/router.env
echo AWS_LINUX_CAPACITY_PROVIDER=${linux_capacityprovider} >> ./properties/router.env
echo AWS_WIN_CAPACITY_PROVIDER=${windows_capacityprovider} >> ./properties/router.env
echo AWS_TARGET_GROUP=${target_group} >> ./properties/router.env
echo S3_BUCKET=${bucket_name} >> ./properties/router.env
echo S3_REGION=${region} >> ./properties/router.env

# scaler.env

# data.env

# start server
./zebrunner.sh start
