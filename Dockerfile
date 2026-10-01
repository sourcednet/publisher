# sourced-publisher in a small image, for checks and CI. Until the sibling
# projects are published, build it from Dev/:
#   docker build -f publisher/Dockerfile -t sourced-publisher .
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY core ./core
COPY publisher ./publisher
COPY testkit ./testkit
COPY resolver ./resolver
WORKDIR /src/publisher
RUN CGO_ENABLED=0 go build -trimpath -o /out/sourced-publisher ./cmd/sourced-publisher

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 sourced
COPY --from=build /out/sourced-publisher /usr/local/bin/sourced-publisher
USER sourced
ENTRYPOINT ["sourced-publisher"]
