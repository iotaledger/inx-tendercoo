#!/bin/bash

if [ ! -d "privatedb" ]; then
  echo "Please run './bootstrap.sh' first"
  exit
fi

docker compose --profile="run" up $@
