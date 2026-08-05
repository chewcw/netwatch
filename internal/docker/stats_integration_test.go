//go:build integration

package docker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fsouza/go-dockerclient"
)

func TestGetStatsAgainstRealDaemon(t *testing.T) {
	client, err := docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		t.Skipf("no docker daemon: %v", err)
	}
	ctx := context.Background()
	ctr, err := client.CreateContainer(docker.CreateContainerOptions{
		Name: "netwatch-it-stats-" + time.Now().Format("150405"),
		Config: &docker.Config{
			Image: "alpine",
			Cmd:   []string{"sh", "-c", "while true; do echo x | nc -u 1.1.1.1 9999; sleep 1; done"},
		},
	})
	if err != nil {
		t.Skipf("cannot create container (pull alpine first?): %v", err)
	}
	defer client.RemoveContainer(docker.RemoveContainerOptions{ID: ctr.ID, Force: true})

	if err := client.StartContainer(ctr.ID, nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := waitRunning(client, ctr.ID, 30*time.Second); err != nil {
		t.Fatalf("wait running: %v", err)
	}

	// Container is running: stats must succeed.
	cl := Client{c: client}
	_, _, err = cl.GetStats(ctx, ctr.ID)
	if err != nil {
		t.Fatalf("GetStats on running container: %v", err)
	}

	// Removed container: must surface as ErrNotFound.
	// NOTE: a STOPPED (but not removed) container is NOT an error path on
	// modern Docker: the daemon returns 200 with zeroed stats (read=
	// zero-time) instead of 404. Only a removed container yields the 404
	// "No such container" that maps to ErrNotFound.
	if err := client.RemoveContainer(docker.RemoveContainerOptions{ID: ctr.ID, Force: true}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	_, _, err = cl.GetStats(ctx, ctr.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetStats on removed container: err = %v, want ErrNotFound", err)
	}
}

func waitRunning(client *docker.Client, id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := client.InspectContainer(id)
		if err == nil && c.State.Running {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("container %s never reached running state", id)
}
