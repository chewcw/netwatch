//go:build integration

package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	dock "github.com/fsouza/go-dockerclient"

	"github.com/chewcw/netwatch/internal/config"
	"github.com/chewcw/netwatch/internal/detector"
	"github.com/chewcw/netwatch/internal/docker"
)

// recordingNotifier captures alerts for inspection in tests.
type recordingNotifier struct {
	mu     sync.Mutex
	alerts []detector.Alert
}

func (r *recordingNotifier) Notify(_ context.Context, a detector.Alert) error {
	r.mu.Lock()
	r.alerts = append(r.alerts, a)
	r.mu.Unlock()
	return nil
}

// startCollectorPair creates two linked containers on the default bridge:
//   - sender:  sends UDP to the alias "receiver" on port 5555 every 1s
//   - receiver: listens on UDP 5555 (rx bytes come from sender traffic)
//
// Returns the receiver container name, the sender container name, and a
// cleanup closure that force-removes both containers.
func startCollectorPair(t *testing.T, client *dock.Client) (receiverName, senderName string, cleanup func()) {
	t.Helper()
	ts := time.Now().Format("150405")

	receiverName = "nw-it-recv-" + ts
	recvCtr, err := client.CreateContainer(dock.CreateContainerOptions{
		Name: receiverName,
		Config: &dock.Config{
			Image: "alpine",
			Cmd:   []string{"sh", "-c", "nc -u -l -p 5555 >/dev/null 2>&1"},
			ExposedPorts: map[dock.Port]struct{}{
				"5555/udp": {},
			},
		},
	})
	if err != nil {
		t.Fatalf("create receiver container: %v", err)
	}

	senderName = "nw-it-send-" + ts
	sendCtr, err := client.CreateContainer(dock.CreateContainerOptions{
		Name: senderName,
		Config: &dock.Config{
			Image: "alpine",
			Cmd:   []string{"sh", "-c", "while true; do echo x | nc -u receiver 5555; sleep 1; done"},
		},
		HostConfig: &dock.HostConfig{
			Links: []string{receiverName + ":receiver"},
		},
	})
	if err != nil {
		t.Fatalf("create sender container: %v", err)
	}

	// Start receiver first so the sender's link target exists at start time.
	if err := client.StartContainer(recvCtr.ID, nil); err != nil {
		t.Fatalf("start receiver: %v", err)
	}
	waitContainerRunning(t, client, recvCtr.ID, 15*time.Second)

	if err := client.StartContainer(sendCtr.ID, nil); err != nil {
		t.Fatalf("start sender: %v", err)
	}
	waitContainerRunning(t, client, sendCtr.ID, 15*time.Second)

	cleanup = func() {
		client.RemoveContainer(dock.RemoveContainerOptions{ID: sendCtr.ID, Force: true})
		client.RemoveContainer(dock.RemoveContainerOptions{ID: recvCtr.ID, Force: true})
	}
	return
}

func waitContainerRunning(t *testing.T, client *dock.Client, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := client.InspectContainer(id)
		if err == nil && c.State.Running {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("container %s did not reach running state within %v", id, timeout)
}

func findContainerID(t *testing.T, client *dock.Client, name string) string {
	t.Helper()
	list, err := client.ListContainers(dock.ListContainersOptions{All: true})
	if err != nil {
		t.Fatalf("list containers: %v", err)
	}
	for _, c := range list {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == name {
				return c.ID
			}
		}
	}
	t.Fatalf("container %q not found on daemon", name)
	return ""
}

func stopContainerByName(t *testing.T, client *dock.Client, name string) {
	t.Helper()
	id := findContainerID(t, client, name)
	if err := client.StopContainer(id, 5); err != nil {
		t.Fatalf("stop container %q: %v", name, err)
	}
}

func startContainerByName(t *testing.T, client *dock.Client, name string) {
	t.Helper()
	id := findContainerID(t, client, name)
	if err := client.StartContainer(id, nil); err != nil {
		t.Fatalf("start container %q: %v", name, err)
	}
	waitContainerRunning(t, client, id, 15*time.Second)
}

func waitForKind(t *testing.T, n *recordingNotifier, kind detector.Kind) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		n.mu.Lock()
		for _, a := range n.alerts {
			if a.Kind == kind {
				n.mu.Unlock()
				return
			}
		}
		// No alert of this kind yet; drop old alerts (they were already
		// consumed by previous waitForKind calls).
		n.alerts = n.alerts[:0]
		n.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for alert kind=%s", kind.String())
}

func TestIntegrationFullFlow(t *testing.T) {
	fClient, err := dock.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		t.Skipf("no docker daemon: %v", err)
	}
	// Quick liveness check.
	if _, err := fClient.ListContainers(dock.ListContainersOptions{All: true, Limit: 1}); err != nil {
		t.Skipf("daemon unreachable: %v", err)
	}

	receiverName, senderName, cleanup := startCollectorPair(t, fClient)
	defer cleanup()

	cfg := config.Config{
		Targets:       []string{receiverName},
		CheckInterval: 2 * time.Second,
		AlertAfter:    6 * time.Second, // ceil(6/2)=3 ticks
		MinTraffic:    0,
		Notify:        []string{"log"},
	}

	stats, err := docker.New("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatalf("docker.New: %v", err)
	}

	n := &recordingNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = Run(ctx, cfg, stats, n) }()

	// Baseline: receiver is receiving from sender. No alerts expected
	// after a few ticks settle (let the seed + first active tick pass).
	time.Sleep(2 * time.Second)
	n.mu.Lock()
	if len(n.alerts) != 0 {
		t.Fatalf("baseline has unexpected alerts: %v", n.alerts)
	}
	n.mu.Unlock()

	// 1. Kill the sender → receiver rx goes silent → Alerted (sensor-side).
	stopContainerByName(t, fClient, senderName)
	waitForKind(t, n, detector.KindAlerted)

	// 2. Restart sender → Recovered.
	startContainerByName(t, fClient, senderName)
	waitForKind(t, n, detector.KindRecovered)

	// 3. Stop receiver → Dead (container exists but not running = ErrStopped).
	stopContainerByName(t, fClient, receiverName)
	waitForKind(t, n, detector.KindDead)

	// 4. Start receiver → Back.
	startContainerByName(t, fClient, receiverName)
	waitForKind(t, n, detector.KindBack)

	cancel()
}
