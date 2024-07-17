#!/bin/bash

user="ubuntu"
e3s_path="/home/$user/tools/e3s"

replace() {
  param=$1
  value=$2
  file=$3

  sed -i -e "s^$param.*^$param=$value^" "$file"
}

sudo apt-get update && sudo apt-get upgrade

# install jq
sudo apt-get -y install jq unzip

# insall aws cli
curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
unzip awscliv2.zip
sudo ./aws/install

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
sudo usermod -aG docker "$user"
# Grant admin rights
sudo usermod -aG sudo "$user"

git config --system --add safe.directory '*'

mkdir -p "$e3s_path"
git clone https://github.com/zebrunner/e3s.git "$e3s_path"

if [ -n "${agent_key}" ]; then
  echo "${agent_key}" > "$e3s_path"/${agent_key_name}.pem
  chmod 400 "$e3s_path"/${agent_key_name}.pem
fi

cd "$e3s_path"
case ${remote_data} in 
  (true) 
    git checkout "terraform-remote"

    # data.env
    replace "POSTGRES_PASSWORD" ${db_pass} "./properties/data.env"
    replace "DATABASE" "postgres://${db_username}:${db_pass}@${db_dns}/${db_name}" "./properties/data.env"
    replace "ELASTIC_CACHE" "${cache_address}:${cache_port}" "./properties/data.env"
    replace "CACHE_REMOTE" "true" "./properties/data.env"
  ;;
  (false)
    git checkout "terraform-local"
  ;;
  (*) 
    echo "remote_data is not a bool value"
  ;;
esac

# config.env
replace "AWS_REGION" ${region} "./properties/config.env"
replace "AWS_CLUSTER" ${cluster_name} "./properties/config.env"
replace "AWS_TASK_ROLE" ${task_role} "./properties/config.env"
replace "AWS_LOGS_GROUP" ${log_group} "./properties/config.env"
replace "S3_BUCKET" ${bucket_name} "./properties/config.env"
replace "S3_REGION" ${bucket_region} "./properties/config.env"
replace "ZEBRUNNER_HOST" ${zbr_host} "./properties/config.env"
replace "ZEBRUNNER_INTEGRATION_USER" ${zbr_user} "./properties/config.env"
replace "ZEBRUNNER_INTEGRATION_PASSWORD" ${zbr_pass} "./properties/config.env"
replace "ZEBRUNNER_ENV" ${env} "./properties/config.env"

# router.env
replace "AWS_LINUX_CAPACITY_PROVIDER" ${linux_capacityprovider} "./properties/router.env"
replace "AWS_WIN_CAPACITY_PROVIDER" ${windows_capacityprovider} "./properties/router.env"
replace "AWS_TARGET_GROUP" ${target_group} "./properties/router.env"

# scaler.env

# task-definitions.env

# start server
./zebrunner.sh start

sudo chown -R "$user" "$e3s_path"