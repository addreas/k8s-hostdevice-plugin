ARG GOLANG_VERSION=1.15
FROM docker.io/golang:${GOLANG_VERSION}-alpine AS builder
LABEL org.opencontainers.image.source https://github.com/addreas/k8s-hostdevice-plugin

ENV \
	OUTDIR='/out' \
	GO111MODULE='on'
RUN set -eux && \
	apk add --no-cache \
	git gcc musl-dev libudev-zero-dev linux-headers
WORKDIR /mod
COPY go.mod /mod/
COPY go.sum /mod/
RUN set -eux && \
	go mod download
COPY . /mod/
RUN set -eux && \
	CGO_ENABLED=1 GOOS=linux GOBIN=${OUTDIR}/usr/bin/ go install \
		-a -v \
		-ldflags="-s -w \"-extldflags=-static\"" \
	.

FROM gcr.io/distroless/static:latest
COPY --from=builder /out/ /
ENTRYPOINT ["/usr/bin/k8s-hostdevice-plugin"]
