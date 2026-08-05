# netwatch

`netwatch` is a small Go application that runs as a Docker container and
watches the network activity of other Docker containers on an IoT edge PC.
It polls the Docker Engine stats API for each configured target container,
tracks the container's received (rx) and transmitted (tx) byte counters, and
fires a local notification when a target stops sending or receiving data for
a configurable window — or when the container itself stops running.

The typical use case: an Azure IoT Edge data-collector module forwards sensor
telemetry to Azure IoT Hub. If the sensor stops producing, or the
collector→cloud path breaks, the data flow silently dies. `netwatch` detects
the gap on the edge device itself and notifies a human locally — no
dependency on the very IoT Hub link being monitored.

## How detection works

Per check interval, `netwatch` fetches each target container's stats and
computes the rx/tx byte deltas. A delta at or below `NETWATCH_MIN_TRAFFIC`
counts as a *silent* tick for that axis (default 0 = strict zero). When an
axis stays silent for `NETWATCH_ALERT_AFTER`, an alert fires — once per
incident. When traffic resumes, a recovery notification fires. A container
that is stopped or removed fires a "not running" alert.

The alert verdict tells you which side of the collector is suspect:

| rx | tx | Meaning |
|---|---|---|
| silent | silent | No data reaching collector — sensor-side suspect |
| active | silent | Collector receiving but not sending — cloud-path suspect |
| silent | active | Collector sending but not receiving (edge case — still alerts) |
| — | — | Collector container not running |

Alert kinds: `alerted` (incident starts), `recovered` (traffic resumed),
`dead` (container not running), `back` (container running again).

## Configuration (environment variables)

| Variable | Default | Meaning |
|---|---|---|
| `NETWATCH_TARGETS` | *(required)* | Comma-separated container names/IDs to watch |
| `NETWATCH_CHECK_INTERVAL` | `30s` | Polling period |
| `NETWATCH_ALERT_AFTER` | `3 × interval` | Silence/dead duration before alerting |
| `NETWATCH_MIN_TRAFFIC` | `0` | Bytes/tick; a delta at or below this counts as silent (set higher to ride over MQTT keepalive noise) |
| `NETWATCH_DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker daemon endpoint |
| `NETWATCH_NOTIFY` | `log` | Notifier backend (only `log` for now; email/Telegram planned) |
| `NETWATCH_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |

Alerts are written as structured log lines to stdout, e.g.:

```
alert target=sensor-collector kind=alerted rx_silent=true tx_silent=true rx_delta=0 tx_delta=0
```

## Usage

### Docker Compose

```yaml
services:
  netwatch:
    image: netwatch:edge
    restart: unless-stopped
    environment:
      NETWATCH_TARGETS: "sensor-collector"
      NETWATCH_CHECK_INTERVAL: "30s"
      NETWATCH_ALERT_AFTER: "90s"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    group_add:
      - "994"   # your host's docker group GID — check: getent group docker
    read_only: true
    security_opt:
      - no-new-privileges:true
```

```sh
docker compose up -d
```

### Build the image

```sh
docker build -t netwatch:edge .
```

### Azure IoT Edge module

Deploy `netwatch:edge` as an IoT Edge module. Set the module environment
variables in the deployment manifest and mount the Docker socket via the
module's createOptions. The image runs as the distroless nonroot user, so it
needs the host docker group GID as a supplementary group (on the device,
`getent group docker` shows it):

```json
"createOptions": {
  "HostConfig": {
    "Binds": ["/var/run/docker.sock:/var/run/docker.sock"],
    "GroupAdd": ["994"]
  }
}
```

Set `NETWATCH_TARGETS` to the name(s) of the module(s) to watch.

## Security note

Mounting `/var/run/docker.sock` gives any process inside this container
root-equivalent access to the host (it can spawn privileged containers).
Acceptable on a dedicated single-purpose edge box. The container is already
hardened: read-only root filesystem, `no-new-privileges`, non-root user, and
no capabilities. The Docker socket mount — plus the docker-group GID needed
only to read it — is the sole elevated access.

## Development

```sh
# unit tests (no daemon needed)
go test ./...

# integration tests (real Docker daemon required; pull alpine first)
docker pull alpine
go test -tags integration ./...
```

Live demo: `sh scripts/demo.sh` starts a fake sensor/collector pair and walks
through the alert, recovery, dead, and back scenarios.
