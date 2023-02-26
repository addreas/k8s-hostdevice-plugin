FROM docker.io/golang:1.20 as build

LABEL org.opencontainers.image.source https://github.com/addreas/k8s-hostdevice-plugin

RUN set -eux && \
	apt-get update && apt-get install -y --no-install-recommends \
	libudev-dev \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /go/src/app

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .
RUN GOBIN=/go/bin go install ./...

# Now copy it into our base image.
FROM gcr.io/distroless/base-debian11:debug
COPY --from=build  /usr/lib/x86_64-linux-gnu/*libudev* /usr/lib/x86_64-linux-gnu/
COPY --from=build /go/bin /bin
ENTRYPOINT ["k8s-hostdevice-plugin"]
