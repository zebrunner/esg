#!/bin/bash

mkdir tools & cd ./tools

git clone https://github.com/zebrunner/esg.git & cd ./e3s

# TODO: implement vars replacement instead of *.env files flushing

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


./zebrunner.sh start
