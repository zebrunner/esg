#!/bin/bash
CONF_FILE="/tmp/redis.conf"

# generate redis.conf file
echo "port 6379
cluster-enabled yes
cluster-config-file nodes.conf
cluster-node-timeout 5000
appendonly yes
auto-aof-rewrite-percentage 10
auto-aof-rewrite-min-size 3gb
loglevel debug
protected-mode no
" >> $CONF_FILE

# start server
redis-server $CONF_FILE