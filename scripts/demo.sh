#!/usr/bin/env sh
# Live demo: starts a fake collector (rx from a fake sensor, tx upstream),
# runs netwatch against it, then demonstrates each alert kind.
set -eu

cd "$(dirname "$0")/.."

docker build -t netwatch:edge . >/dev/null

docker rm -f nw-demo-sensor nw-demo-collector nw-demo-cloud 2>/dev/null || true
docker network create nw-demo 2>/dev/null || true

# Cloud-side listener: the collector's upstream sends go here. busybox nc -u -l
# is one-shot (exits after the first datagram) and binds only [::], so use socat
# (persistent, discards, no ICMP port-unreachable replies back to the collector).
docker run -d --name nw-demo-cloud --network nw-demo \
  alpine sh -c 'apk add --no-cache socat >/dev/null 2>&1; exec socat -u UDP-RECV:9999 -' >/dev/null

# Sensor: 2kB UDP datagrams every 1s (clearly above MIN_TRAFFIC_RX).
docker run -d --name nw-demo-sensor --network nw-demo \
  alpine sh -c 'while true; do head -c 2000 /dev/zero | nc -u -w 1 nw-demo-collector 5555; sleep 1; done' >/dev/null

# Collector: listens for the sensor on 5555 AND forwards 2kB datagrams upstream
# to the cloud listener every 1s. MIN_TRAFFIC_RX/TX tolerates ambient host multicast
# (SSDP/mDNS) that Docker delivers to every bridge container — strict-zero
# silence would be unreachable on a busy host.
docker run -d --name nw-demo-collector --network nw-demo \
  alpine sh -c 'while true; do nc -u -l -s 0.0.0.0 -p 5555 >/dev/null; done & while true; do head -c 2000 /dev/zero | nc -u -w 1 nw-demo-cloud 9999; sleep 1; done' >/dev/null

echo "== starting netwatch (interval 2s, alert after 6s) =="
DOCKER_GID=$(getent group docker | cut -d: -f3)
docker run --rm --name nw-demo-netwatch \
  -e NETWATCH_TARGETS=nw-demo-collector \
  -e NETWATCH_CHECK_INTERVAL=2s \
  -e NETWATCH_ALERT_AFTER=6s \
  -e NETWATCH_MIN_TRAFFIC_RX=2000 \
  -e NETWATCH_MIN_TRAFFIC_TX=2000 \
  --group-add "$DOCKER_GID" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  netwatch:edge > /tmp/netwatch-demo.log 2>&1 &
NETWATCH_PID=$!

sleep 12
echo "== killing sensor (expect Alerted — rx silent; tx stays active) =="
docker stop nw-demo-sensor >/dev/null
sleep 12
echo "== restarting sensor (expect Recovered) =="
docker start nw-demo-sensor >/dev/null
sleep 12
echo "== stopping collector (expect Dead) =="
docker stop nw-demo-collector >/dev/null
sleep 12
echo "== starting collector (expect Back) =="
docker start nw-demo-collector >/dev/null
sleep 12

kill $NETWATCH_PID 2>/dev/null || true
wait $NETWATCH_PID 2>/dev/null || true

echo
echo "===== netwatch log ====="
grep -E 'kind=' /tmp/netwatch-demo.log || echo "(no alerts captured)"

docker rm -f nw-demo-sensor nw-demo-collector nw-demo-cloud nw-demo-netwatch 2>/dev/null || true
docker network rm nw-demo 2>/dev/null || true
