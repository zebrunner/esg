#!/bin/bash

#./build.sh

#rm -f esg.log
#nohup ./esg -retry-count 2 -aws-elastic-cache esg-redis2.t0rrbb.ng.0001.use1.cache.amazonaws.com:6379 -listen :4445 -s3-bucket zebrunner.dev-assets >> ./esg.log 2>&1 &
#nohup ./esg -retry-count 2 -aws-elastic-cache esg-redis2.t0rrbb.ng.0001.use1.cache.amazonaws.com:6379 -listen :4444 -s3-bucket zebrunner.dev-assets >> ./esg.log 2>&1 &

docker-compose up -d --build
