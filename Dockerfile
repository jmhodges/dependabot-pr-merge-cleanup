FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod ./
COPY go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /dependabot-pr-cleanup .

FROM alpine:3.19
COPY --from=build /dependabot-pr-cleanup /usr/local/bin/
ENTRYPOINT ["dependabot-pr-cleanup"]
