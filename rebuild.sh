#!/bin/bash
set -ex
go build
docker build -t quay.io/pingthingsio/sentry-gateway:v2 .
docker push quay.io/pingthingsio/sentry-gateway:v2
