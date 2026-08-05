package docker

import (
	"context"
	"errors"
	"fmt"

	"github.com/fsouza/go-dockerclient"
)

var (
	ErrNotFound = errors.New("container not found")
	ErrStopped  = errors.New("container not running")
)

type StatsClient interface {
	GetStats(ctx context.Context, name string) (rx, tx uint64, err error)
}

type Client struct {
	c *docker.Client
}

func New(host string) (*Client, error) {
	c, err := docker.NewClient(host)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Client{c: c}, nil
}

// GetStats returns the aggregate rx/tx byte counts across all of the
// container's network interfaces. ErrNotFound if the container does not
// exist (removed); ErrStopped if it exists but is not running — modern
// Docker returns zeroed stats for stopped containers, so running state is
// checked explicitly via inspect (zeroed stats would otherwise read as
// silence, not death).
func (c *Client) GetStats(ctx context.Context, name string) (uint64, uint64, error) {
	rx, tx, err := c.fetchStats(ctx, name)
	if err != nil {
		return 0, 0, err
	}
	ctr, err := c.c.InspectContainerWithOptions(docker.InspectContainerOptions{Context: ctx, ID: name})
	if err != nil {
		return 0, 0, classifyError(err)
	}
	if !ctr.State.Running {
		return 0, 0, ErrStopped
	}
	return rx, tx, nil
}

// fetchStats fetches one non-streaming stats sample and returns the
// aggregate rx/tx byte counts across all of the container's network
// interfaces.
func (c *Client) fetchStats(ctx context.Context, name string) (uint64, uint64, error) {
	ch := make(chan *docker.Stats, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.c.Stats(docker.StatsOptions{
			ID:      name,
			Stream:  false,
			Stats:   ch,
			Context: ctx,
		})
	}()

	var s *docker.Stats
	var ok bool
	select {
	case s, ok = <-ch:
	case <-ctx.Done():
		return 0, 0, ctx.Err()
	}
	if !ok {
		// channel closed without a sample (e.g. 404): fetch the error.
		err := <-errCh
		return 0, 0, classifyError(err)
	}
	err := <-errCh
	if err != nil {
		return 0, 0, classifyError(err)
	}
	return s.Network.RxBytes, s.Network.TxBytes, nil
}

func classifyError(err error) error {
	if err == nil {
		return nil
	}
	var nsc *docker.NoSuchContainer
	if errors.As(err, &nsc) {
		return ErrNotFound
	}
	return err
}
