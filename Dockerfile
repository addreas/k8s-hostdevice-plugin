ARG GOLANG_VERSION=1.15
FROM docker.io/golang:${GOLANG_VERSION}-alpine AS builder

ENV \
	OUTDIR='/out' \
	GO111MODULE='on'
RUN set -eux && \
	apk add --no-cache \
		git
WORKDIR /mod
COPY go.mod /mod/
COPY go.sum /mod/
RUN set -eux && \
	go mod download
COPY . /mod/
RUN set -eux && \
	CGO_ENABLED=0 GOBIN=${OUTDIR}/usr/bin/ go install \
		-a -v \
		-tags='osusergo,netgo' \
		-installsuffix='netgo' \
		-ldflags="-s -w \"-extldflags=-static\"" \
	.

FROM gcr.io/distroless/static:latest
COPY --from=builder /out/ /
ENTRYPOINT ["/usr/bin/k8s-hostdevice-plugin"]
