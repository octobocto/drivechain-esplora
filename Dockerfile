FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /drivechain-esplora ./cmd/drivechain-esplora

FROM alpine:3.20
RUN adduser -D -u 10001 esplora
COPY --from=build /drivechain-esplora /usr/local/bin/drivechain-esplora
USER esplora
ENTRYPOINT ["/usr/local/bin/drivechain-esplora"]
