#!/bin/sh
set -eu
umask 077
set -C
sed -e "s/^APP_TOKEN=$/APP_TOKEN=$(openssl rand -hex 32)/" \
    -e "s/^POSTGRES_PASSWORD=$/POSTGRES_PASSWORD=$(openssl rand -hex 24)/" .env.example > .env
