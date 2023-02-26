FROM docker.io/golang:1.20 AS builder
LABEL org.opencontainers.image.source https://github.com/addreas/k8s-hostdevice-plugin

RUN set -eux && \
	apt-get update && apt-get install -y --no-install-recommends \
	libudev-dev \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /mod

COPY go.mod /mod/
COPY go.sum /mod/
RUN go mod download

COPY . /mod/

RUN CGO_ENABLED=1 GOOS=linux GOBIN=/bin go install ./...

RUN apt-get purge --autoremove gcc g++ git gpg wget curl subversion python3 openssh-client perl

ENTRYPOINT ["k8s-hostdevice-plugin"]
