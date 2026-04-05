#
# ClawSSH — production Docker image (multi-stage)
#

FROM golang:1.22-alpine AS build
WORKDIR /src

RUN apk add --no-cache git ca-certificates && update-ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/clawssh ./cmd/clawssh

FROM alpine:3.20 AS run
RUN apk add --no-cache ca-certificates tzdata && update-ca-certificates

WORKDIR /data
COPY --from=build /out/clawssh /usr/local/bin/clawssh

EXPOSE 2222/tcp

# Default runtime paths inside container
ENV CLAWSSH_ADDR=:2222 \
    CLAWSSH_HOST_KEY=/data/host_key \
    CLAWSSH_POLICIES=/data/policies.yaml \
    CLAWSSH_INVENTORY=/data/hosts \
    CLAWSSH_KNOWLEDGE_PATH=/data/docs/ops \
    CLAWSSH_ENV=production

ENTRYPOINT ["/usr/local/bin/clawssh"]
