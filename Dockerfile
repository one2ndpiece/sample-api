# syntax=docker/dockerfile:1

FROM golang:1.26.4-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/sample-api ./cmd/sample-api

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/sample-api /sample-api
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/sample-api"]
