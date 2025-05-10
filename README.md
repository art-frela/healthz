# healthz

[![Tests](https://github.com/art-frela/healthz/actions/workflows/go.yml/badge.svg)](https://github.com/art-frela/healthz/actions)
[![Coverage](https://codecov.io/gh/art-frela/healthz/branch/main/graph/badge.svg)](https://codecov.io/gh/art-frela/healthz)
[![Version](https://img.shields.io/github/v/tag/art-frela/healthz?label=version&sort=semver)](https://github.com/art-frela/healthz/tags)

> Healthz is a Go package that simplifies service health monitoring by letting dependencies implement a unified interface, grouping them into startup, liveness, and readiness checks, and automating periodic status updates for seamless integration with Kubernetes-style health endpoints.

- [healthz](#healthz)
  - [Status](#status)
  - [Description](#description)
    - [How to use](#how-to-use)

## Status

**dev time-to-time**

## Description

### How to use

- Implement interface `healthz.HealthCheckable` for each dependency whose health needs to be checked
- Create healthz.Inspector with `healthz.HealthCheckable`
  - if need influence to the probe, please specify 
    - for startup - `healthz.GroupStartup`
    - for liveness - `healthz.GroupLive`
    - for rediness - `healthz.GroupReady`
    - for combinations (use `OR`) - `healthz.GroupStartup | healthz.GroupLive`
    - if need all - `healthz.AllGroups`
  - for simple periodically check health and update metric - `healthz.GroupCommon`
- You could specify `prometheus.GaugeVec` metric with two variable labels: "scope", "dest" `err := healthz.WithMetric(<metric>)(<*inspector>)`
- And could specify period for health check `err := healthz.WithCheckPeriod(<period>)(<*inspector>)` , default 15s
- Need call `Inspector.Start(context.Context) error` method for running periodically health checks
- In end call `Inspector.Stop(context.Context) error` method for stopping periodically health checks

[example](./example/stdusecase/stdusecase.go)

```go
// GET /metrics
// # HELP service_up The example metric
// # TYPE service_up gauge
// service_up{dest="host-1:5432/db_1",foo="bar",scope="database"} 0
// service_up{dest="host-1:8431",foo="bar",scope="kafka"} 1

// GET /healthz/startup
// OK

// GET /healthz/live
// OK

// GET /healthz/ready
// Unhealthy
```
