package server

import (
	"sync"

	"github.com/ng-life/zookeeper-explorer/internal/config"
	"github.com/ng-life/zookeeper-explorer/internal/zookeeper"
)

type clientSlot struct {
	sync.Mutex
	client zookeeper.Client
}

// Server owns HTTP handlers and lazily cached cluster connections.
type Server struct {
	config config.Config
	slots  []clientSlot
	dial   func(string) (zookeeper.Client, error)
}

// New creates a server without opening ZooKeeper connections.
func New(c config.Config) *Server {
	return &Server{config: c, slots: make([]clientSlot, len(c.Clusters)), dial: zookeeper.Connect}
}
func (a *Server) client(id int) (zookeeper.Client, error) {
	s := &a.slots[id]
	s.Lock()
	defer s.Unlock()
	if s.client == nil {
		c, err := a.dial(a.config.Clusters[id].Address)
		if err != nil {
			return nil, err
		}
		s.client = c
	}
	return s.client, nil
}

// Close releases every cached ZooKeeper connection after HTTP shutdown.
func (a *Server) Close() {
	for i := range a.slots {
		s := &a.slots[i]
		s.Lock()
		if s.client != nil {
			s.client.Close()
		}
		s.Unlock()
	}
}
