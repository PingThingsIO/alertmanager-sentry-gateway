FROM ubuntu:xenial
MAINTAINER Moto Ishizawa "summerwind.jp"

COPY ./alertmanager-sentry-gateway /bin/alertmanager-sentry-gateway

ENTRYPOINT ["/bin/alertmanager-sentry-gateway"]
