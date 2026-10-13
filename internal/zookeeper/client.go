package zookeeper

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-zookeeper/zk"
)

// NormalizePath validates a node path and removes its trailing slash.
func NormalizePath(p string) (string, error) {
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "//") {
		return "", fmt.Errorf("Invalid ZooKeeper path")
	}
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == ".." {
			return "", fmt.Errorf("Invalid ZooKeeper path")
		}
	}
	for _, r := range p {
		if r == 0 || r >= 1 && r <= 31 || r >= 127 && r <= 159 || r >= 0xd800 && r <= 0xf8ff || r >= 0xfff0 && r <= 0xffff {
			return "", fmt.Errorf("Invalid ZooKeeper path")
		}
	}
	if p != "/" {
		p = strings.TrimSuffix(p, "/")
	}
	return p, nil
}

// The Go client has no chroot option; prefix every operation in this adapter.
func ParseAddress(address string) ([]string, string, error) {
	hosts, root, hasRoot := strings.Cut(address, "/")
	rootPath := ""
	if hasRoot {
		var err error
		rootPath, err = NormalizePath("/" + root)
		if err != nil {
			return nil, "", err
		}
		if rootPath == "/" {
			rootPath = ""
		}
	}
	servers := strings.Split(hosts, ",")
	for _, server := range servers {
		if strings.TrimSpace(server) == "" {
			return nil, "", fmt.Errorf("empty ZooKeeper server")
		}
	}
	return servers, rootPath, nil
}

// Client is the node access interface used by the HTTP server.
type Client interface {
	Children(string) ([]string, *zk.Stat, error)
	Get(string) ([]byte, *zk.Stat, error)
	Exists(string) (bool, *zk.Stat, error)
	Delete(string, int32) error
	Close()
}
type rootedClient struct {
	conn *zk.Conn
	root string
}

func (c *rootedClient) path(p string) string {
	if p == "/" && c.root != "" {
		return c.root
	}
	return c.root + p
}
func (c *rootedClient) Children(p string) ([]string, *zk.Stat, error) {
	return c.conn.Children(c.path(p))
}
func (c *rootedClient) Get(p string) ([]byte, *zk.Stat, error)  { return c.conn.Get(c.path(p)) }
func (c *rootedClient) Exists(p string) (bool, *zk.Stat, error) { return c.conn.Exists(c.path(p)) }
func (c *rootedClient) Delete(p string, v int32) error          { return c.conn.Delete(c.path(p), v) }
func (c *rootedClient) Close()                                  { c.conn.Close() }

// Connect establishes a session and applies the optional chroot to operations.
func Connect(address string) (Client, error) {
	return connect(context.Background(), address, false)
}

// ConnectContext bounds session establishment and suppresses driver diagnostics.
func ConnectContext(ctx context.Context, address string) (Client, error) {
	return connect(ctx, address, true)
}

func connect(ctx context.Context, address string, quiet bool) (Client, error) {
	servers, root, err := ParseAddress(address)
	if err != nil {
		return nil, err
	}
	var conn *zk.Conn
	var events <-chan zk.Event
	if quiet {
		conn, events, err = zk.Connect(servers, 10*time.Second, zk.WithLogger(quietLogger{}))
	} else {
		conn, events, err = zk.Connect(servers, 10*time.Second)
	}
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				conn.Close()
				return nil, fmt.Errorf("connection closed")
			}
			if event.State == zk.StateHasSession {
				go func() {
					for range events {
					}
				}()
				return &rootedClient{conn, root}, nil
			}
		case <-ctx.Done():
			conn.Close()
			return nil, ctx.Err()
		case <-timer.C:
			conn.Close()
			return nil, fmt.Errorf("connection timed out")
		}
	}
}

type quietLogger struct{}

func (quietLogger) Printf(string, ...interface{}) {}
