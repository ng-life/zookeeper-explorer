package config

import (
	"flag"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/ng-life/zookeeper-explorer/internal/zookeeper"
	"github.com/pelletier/go-toml/v2"
)

// Cluster describes one named ZooKeeper ensemble.
type Cluster struct {
	Name    string `toml:"name" json:"name"`
	Address string `toml:"address" json:"address"`
}

// Config holds server settings and configured ensembles.
type Config struct {
	Listen      string    `toml:"listen"`
	AllowDelete bool      `toml:"allow_delete"`
	Clusters    []Cluster `toml:"clusters"`
}
type stringFlags []string

func (s *stringFlags) String() string     { return strings.Join(*s, ",") }
func (s *stringFlags) Set(v string) error { *s = append(*s, v); return nil }

// Load reads TOML configuration and applies command-line overrides.
func Load(args []string) (Config, error) {
	c := Config{Listen: "127.0.0.1:8080", Clusters: []Cluster{}}
	fs := flag.NewFlagSet("zookeeper-explorer", flag.ContinueOnError)
	var file, listen string
	var addresses stringFlags
	var enable, disable bool
	fs.StringVar(&file, "config", "config.toml", "TOML configuration file")
	fs.StringVar(&file, "c", "config.toml", "TOML configuration file (short)")
	fs.StringVar(&listen, "listen", "", "Web server bind address")
	fs.Var(&addresses, "zk", "ZooKeeper ensemble NAME=HOSTS (repeatable)")
	fs.BoolVar(&enable, "allow-delete", false, "Enable recursive node deletion")
	fs.BoolVar(&disable, "disable-delete", false, "Disable recursive node deletion")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() != 0 {
		return c, fmt.Errorf("unexpected positional arguments")
	}
	if enable && disable {
		return c, fmt.Errorf("--allow-delete conflicts with --disable-delete")
	}
	raw, err := os.ReadFile(file)
	if err == nil {
		if err = toml.Unmarshal(raw, &c); err != nil {
			return c, err
		}
	} else if !os.IsNotExist(err) {
		return c, err
	}
	if listen != "" {
		c.Listen = listen
	}
	if len(addresses) > 0 {
		c.Clusters = []Cluster{}
		for _, item := range addresses {
			name, address, ok := strings.Cut(item, "=")
			if !ok {
				address = name
			}
			c.Clusters = append(c.Clusters, Cluster{name, address})
		}
	}
	if enable {
		c.AllowDelete = true
	}
	if disable {
		c.AllowDelete = false
	}
	if _, err := net.ResolveTCPAddr("tcp", c.Listen); err != nil {
		return c, err
	}
	for _, cl := range c.Clusters {
		if _, _, err := zookeeper.ParseAddress(cl.Address); err != nil {
			return c, err
		}
	}
	return c, nil
}
