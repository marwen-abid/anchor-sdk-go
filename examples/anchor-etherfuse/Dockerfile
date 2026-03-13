FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /anchor ./examples/anchor-etherfuse

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /anchor /anchor
ENTRYPOINT ["/anchor"]
