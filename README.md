# sample-api

`sample-api` is a small Go service used to verify that an application in a separate repository can be deployed by the `learn-k8s` GitOps platform.

It exposes:

- Public HTTP on `:8080`
- Prometheus metrics on `:9090/metrics`
- Structured JSON logs on stdout
- `/`, `/healthz`, `/readyz`, `/slow`, `/error`, and `/cpu` endpoints

Kubernetes manifests live under `deploy/k8s`. The lab cluster overlay is `deploy/k8s/overlays/lab`.

## Development Environment

This repository has its own Nix dev shell. From this directory:

```sh
direnv allow
```

Or enter it manually:

```sh
nix develop
```

## Local Run

```sh
go test ./...
go run ./cmd/sample-api
```

## Container

```sh
docker build -t sample-api:local .
docker run --rm -p 8080:8080 -p 9090:9090 sample-api:local
```
