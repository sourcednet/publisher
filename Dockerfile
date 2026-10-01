# sourced-publisher in a small image, for checks and CI:
#   docker build -t sourced-publisher .
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN GOWORK=off go mod download
COPY . .
RUN GOWORK=off CGO_ENABLED=0 GOFLAGS=-mod=mod go build -trimpath -o /out/sourced-publisher ./cmd/sourced-publisher

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 sourced
COPY --from=build /out/sourced-publisher /usr/local/bin/sourced-publisher
USER sourced
ENTRYPOINT ["sourced-publisher"]
