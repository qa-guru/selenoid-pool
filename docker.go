package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// dockerClient is the minimal Docker Engine API surface the pool needs for
// warm-slot rotation: POST /containers/{name}/restart over the unix socket.
// No moby dependency — the orchestrator image stays a single static binary.
type dockerClient struct {
	sock string
	http *http.Client
}

func newDockerClient(sockPath string) *dockerClient {
	return &dockerClient{
		sock: sockPath,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", sockPath)
				},
			},
		},
	}
}

// RestartContainer restarts a container by name with a 10s stop timeout.
func (d *dockerClient) RestartContainer(ctx context.Context, name string) error {
	u := fmt.Sprintf("http://docker/containers/%s/restart?t=10", url.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("container %q not found", name)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("restart %s -> HTTP %d: %s", name, resp.StatusCode, string(raw))
	}
	return nil
}
