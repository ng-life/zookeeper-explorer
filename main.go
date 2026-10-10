package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/go-zookeeper/zk"
	"github.com/pelletier/go-toml/v2"
)

//go:embed static/index.html
var indexHTML []byte

type cluster struct {
	Name    string `toml:"name" json:"name"`
	Address string `toml:"address" json:"address"`
}
type config struct {
	Listen      string    `toml:"listen"`
	AllowDelete bool      `toml:"allow_delete"`
	Clusters    []cluster `toml:"clusters"`
}
type stringFlags []string

func (s *stringFlags) String() string     { return strings.Join(*s, ",") }
func (s *stringFlags) Set(v string) error { *s = append(*s, v); return nil }

func loadConfig(args []string) (config, error) {
	c := config{Listen: "127.0.0.1:8080", Clusters: []cluster{}}
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
		c.Clusters = []cluster{}
		for _, item := range addresses {
			name, address, ok := strings.Cut(item, "=")
			if !ok {
				address = name
			}
			c.Clusters = append(c.Clusters, cluster{name, address})
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
		if _, _, err := parseAddress(cl.Address); err != nil {
			return c, err
		}
	}
	return c, nil
}

func normalizePath(p string) (string, error) {
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
func parseAddress(address string) ([]string, string, error) {
	hosts, root, hasRoot := strings.Cut(address, "/")
	rootPath := ""
	if hasRoot {
		var err error
		rootPath, err = normalizePath("/" + root)
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

type nodeClient interface {
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
func connect(address string) (nodeClient, error) {
	servers, root, err := parseAddress(address)
	if err != nil {
		return nil, err
	}
	conn, events, err := zk.Connect(servers, 10*time.Second)
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
		case <-timer.C:
			conn.Close()
			return nil, fmt.Errorf("connection timed out")
		}
	}
}

type clientSlot struct {
	sync.Mutex
	client nodeClient
}
type app struct {
	config config
	slots  []clientSlot
	dial   func(string) (nodeClient, error)
}

func newApp(c config) *app {
	return &app{config: c, slots: make([]clientSlot, len(c.Clusters)), dial: connect}
}
func (a *app) client(id int) (nodeClient, error) {
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
func (a *app) close() {
	for i := range a.slots {
		s := &a.slots[i]
		s.Lock()
		if s.client != nil {
			s.client.Close()
		}
		s.Unlock()
	}
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

type nodeEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}
type nodeResponse struct {
	Path       string      `json:"path"`
	Nodes      []nodeEntry `json:"nodes"`
	Data       *string     `json:"data"`
	DataBase64 *string     `json:"data_base64"`
	DataLen    int32       `json:"data_len"`
}

func (a *app) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/clusters", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.config.Clusters) })
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"allow_delete": a.config.AllowDelete})
	})
	mux.HandleFunc("GET /api/clusters/{id}/nodes", a.nodes)
	mux.HandleFunc("GET /api/clusters/{id}/node/metadata", a.metadata)
	mux.HandleFunc("GET /api/clusters/{id}/node/download", a.download)
	mux.HandleFunc("DELETE /api/clusters/{id}/nodes", a.delete)
	return mux
}
func (a *app) request(w http.ResponseWriter, r *http.Request) (nodeClient, string, bool) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "/"
	}
	p, err := normalizePath(p)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return nil, "", false
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 0 || id >= len(a.config.Clusters) {
		http.Error(w, "Unknown cluster", 404)
		return nil, "", false
	}
	c, err := a.client(id)
	if err != nil {
		http.Error(w, "ZooKeeper connection failed: "+err.Error(), 502)
		return nil, "", false
	}
	return c, p, true
}
func (a *app) nodes(w http.ResponseWriter, r *http.Request) {
	c, p, ok := a.request(w, r)
	if !ok {
		return
	}
	children, _, err := c.Children(p)
	if err != nil {
		http.Error(w, fmt.Sprintf("Could not list %s: %v", p, err), 502)
		return
	}
	sort.SliceStable(children, func(i, j int) bool { return strings.ToLower(children[i]) < strings.ToLower(children[j]) })
	out := nodeResponse{Path: p, Nodes: []nodeEntry{}}
	for _, name := range children {
		out.Nodes = append(out.Nodes, nodeEntry{name, strings.TrimSuffix(p, "/") + "/" + name})
	}
	if p != "/" {
		data, stat, err := c.Get(p)
		if err != nil {
			http.Error(w, fmt.Sprintf("Could not read %s: %v", p, err), 502)
			return
		}
		out.DataLen = stat.DataLength
		if utf8.Valid(data) {
			s := string(data)
			out.Data = &s
		} else {
			s := base64.StdEncoding.EncodeToString(data)
			out.DataBase64 = &s
		}
	}
	writeJSON(w, out)
}
func (a *app) metadata(w http.ResponseWriter, r *http.Request) {
	c, p, ok := a.request(w, r)
	if !ok {
		return
	}
	exists, stat, err := c.Exists(p)
	if err != nil {
		http.Error(w, fmt.Sprintf("Could not inspect %s: %v", p, err), 502)
		return
	}
	if !exists {
		http.Error(w, "Node not found: "+p, 404)
		return
	}
	writeJSON(w, map[string]any{"is_dir": stat.NumChildren > 0, "data_len": stat.DataLength, "children_count": stat.NumChildren})
}
func (a *app) download(w http.ResponseWriter, r *http.Request) {
	// Reject the root before opening a connection.
	if p := r.URL.Query().Get("path"); p == "" || p == "/" {
		http.Error(w, "Cannot download the root node", 400)
		return
	}
	c, p, ok := a.request(w, r)
	if !ok {
		return
	}
	data, _, err := c.Get(p)
	if err != nil {
		http.Error(w, fmt.Sprintf("Could not read %s: %v", p, err), 502)
		return
	}
	name := p[strings.LastIndex(p, "/")+1:]
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+strings.ReplaceAll(url.QueryEscape(name), "+", "%20"))
	w.Write(data)
}
func (a *app) delete(w http.ResponseWriter, r *http.Request) {
	if !a.config.AllowDelete {
		http.Error(w, "Recursive deletion is disabled. Enable allow_delete in config or pass --allow-delete.", 403)
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" || p == "/" {
		http.Error(w, "Cannot delete the root node", 400)
		return
	}
	c, p, ok := a.request(w, r)
	if !ok {
		return
	}
	type entry struct {
		path    string
		visited bool
	}
	stack := []entry{{p, false}}
	deleted := 0
	// Post-order traversal avoids recursion depth limits and deletes parents last.
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.visited {
			if err := c.Delete(n.path, -1); err != nil {
				http.Error(w, fmt.Sprintf("Recursive delete stopped at %s: %v; %d child node(s) already deleted", n.path, err, deleted), 409)
				return
			}
			deleted++
			continue
		}
		children, _, err := c.Children(n.path)
		if err != nil {
			http.Error(w, fmt.Sprintf("Could not inspect %s for recursive deletion: %v; %d child node(s) already deleted", n.path, err, deleted), 502)
			return
		}
		stack = append(stack, entry{n.path, true})
		for _, child := range children {
			stack = append(stack, entry{n.path + "/" + child, false})
		}
	}
	writeJSON(w, map[string]int{"deleted_nodes": deleted})
}
func run() error {
	c, err := loadConfig(os.Args[1:])
	if err != nil {
		return err
	}
	a := newApp(c)
	defer a.close()
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Printf("ZooKeeper Explorer is ready at %s", listener.Addr())
	select {
	case err := <-done:
		if err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			server.Close()
			return err
		}
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		if err == flag.ErrHelp {
			return
		}
		log.Fatal(err)
	}
}
