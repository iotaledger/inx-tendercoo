#!/bin/bash

if [[ "$OSTYPE" != "darwin"* && "$EUID" -ne 0 ]]; then
  echo "Please run as root or with sudo"
  exit
fi

# Cleanup if necessary
if [ -d "privatedb" ] || [ -d "snapshots" ]; then
  ./cleanup.sh
fi

if [[ $1 = "build" ]]; then
  # Build latest code
  docker-compose build
fi

# Create snapshot
mkdir -p snapshots/coo-1
if [[ "$OSTYPE" != "darwin"* ]]; then
  chown -R 65532:65532 snapshots
fi
docker-compose run create-snapshots

# Duplicate snapshot for all nodes
cp -R snapshots/coo-1 snapshots/coo-2
cp -R snapshots/coo-1 snapshots/coo-3
cp -R snapshots/coo-1 snapshots/coo-4
cp -R snapshots/coo-1 snapshots/hornet-1
if [[ "$OSTYPE" != "darwin"* ]]; then
  chown -R 65532:65532 snapshots
fi

# Prepare database directory
mkdir -p privatedb/coo-1
mkdir -p privatedb/tendermint-1
mkdir -p privatedb/coo-2
mkdir -p privatedb/tendermint-2
mkdir -p privatedb/coo-3
mkdir -p privatedb/tendermint-3
mkdir -p privatedb/coo-4
mkdir -p privatedb/tendermint-4
mkdir -p privatedb/hornet-1
if [[ "$OSTYPE" != "darwin"* ]]; then
  chown -R 65532:65532 privatedb
fi

docker-compose --profile="bootstrap" up