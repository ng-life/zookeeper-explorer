package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ng-life/zookeeper-explorer/internal/web"
	"github.com/ng-life/zookeeper-explorer/internal/zookeeper"
)

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

// Handler returns the browser and API routes.
func (a *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(web.IndexHTML)
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
func (a *Server) request(w http.ResponseWriter, r *http.Request) (zookeeper.Client, string, bool) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "/"
	}
	p, err := zookeeper.NormalizePath(p)
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
func (a *Server) nodes(w http.ResponseWriter, r *http.Request) {
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
func (a *Server) metadata(w http.ResponseWriter, r *http.Request) {
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
func (a *Server) download(w http.ResponseWriter, r *http.Request) {
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
func (a *Server) delete(w http.ResponseWriter, r *http.Request) {
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
