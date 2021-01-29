ARG GOLANG_VERSION=1.15
FROM docker.io/golang:${GOLANG_VERSION} AS builder
LABEL org.opencontainers.image.source https://github.com/addreas/k8s-hostdevice-plugin

ENV \
	OUTDIR='/out' \
	GO111MODULE='on'

RUN set -eux && \
	apt-get update && apt-get install -y --no-install-recommends \
	libudev-dev \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /mod

COPY go.mod /mod/
COPY go.sum /mod/
RUN set -eux && \
	go mod download
COPY . /mod/
RUN set -eux && \
	CGO_ENABLED=1 GOOS=linux GOBIN=/bin go install \
	./...
RUN rm -r /go /usr

FROM scratch
COPY --from=builder / /
ENTRYPOINT ["k8s-hostdevice-plugin"]
