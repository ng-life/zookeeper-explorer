package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-zookeeper/zk"
	"github.com/ng-life/zookeeper-explorer/internal/config"
)

// Opt in only against an isolated local ZooKeeper instance. This test creates
// its own subtree and exercises deletion there; it never accepts a remote host.
func TestZooKeeperIntegration(t *testing.T) {
	address := os.Getenv("ZK_TEST_ADDRESS")
	if address == "" {
		t.Skip("set ZK_TEST_ADDRESS to an isolated loopback ZooKeeper instance")
	}
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		t.Fatal("ZK_TEST_ADDRESS must be a loopback host:port without chroot")
	}
	seed, events, err := zk.Connect([]string{address}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	go func() {
		for range events {
		}
	}()
	root := fmt.Sprintf("/explorer-integration-%d", time.Now().UnixNano())
	text := []byte(`{"message":"你好"}`)
	binary := []byte{0xff, 0x00, 0xfe}
	fixtures := []struct {
		path string
		data []byte
	}{{root, nil}, {root + "/目录", nil}, {root + "/目录/配置.json", text}, {root + "/目录/binary", binary}, {root + "/empty", nil}}
	a := New(config.Config{Clusters: []config.Cluster{{Name: "local", Address: address}, {Name: "chroot", Address: address + root}}, AllowDelete: true})
	defer a.Close()
	defer func() {
		w := request(a, "DELETE", "/api/clusters/0/nodes?"+url.Values{"path": {root}}.Encode())
		if w.Code != 200 && !strings.Contains(w.Body.String(), "node does not exist") {
			t.Errorf("fixture cleanup: %d %s", w.Code, w.Body.String())
		}
	}()
	for _, fixture := range fixtures {
		if _, err := seed.Create(fixture.path, fixture.data, 0, zk.WorldACL(zk.PermAll)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		id     int
		prefix string
	}{{0, root}, {1, ""}} {
		for _, item := range []struct {
			name string
			data []byte
		}{{"配置.json", text}, {"binary", binary}} {
			p := tc.prefix + "/目录/" + item.name
			query := url.Values{"path": {p}}.Encode()
			endpoint := fmt.Sprintf("/api/clusters/%d", tc.id)
			w := request(a, "GET", endpoint+"/nodes?"+query)
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			var out nodeResponse
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Path != p || len(out.Nodes) != 0 || out.DataLen != int32(len(item.data)) {
				t.Fatalf("unexpected node: %+v", out)
			}
			if item.name == "binary" {
				if out.Data != nil || out.DataBase64 == nil || *out.DataBase64 != base64.StdEncoding.EncodeToString(binary) {
					t.Fatal("binary mismatch")
				}
			} else if out.Data == nil || *out.Data != string(text) {
				t.Fatal("text mismatch")
			}
			w = request(a, "GET", endpoint+"/node/download?"+query)
			if w.Code != 200 || w.Body.String() != string(item.data) {
				t.Fatal("download mismatch")
			}
			if item.name == "配置.json" && !strings.Contains(w.Header().Get("Content-Disposition"), "%E9%85%8D%E7%BD%AE.json") {
				t.Fatal(w.Header())
			}
		}
	}
	w := request(a, "GET", "/api/clusters/1/nodes")
	var out nodeResponse
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Path != "/" || out.Data != nil || len(out.Nodes) != 2 || out.Nodes[0].Path != "/empty" {
		t.Fatalf("chroot root mismatch: %+v", out)
	}
	w = request(a, "GET", "/api/clusters/1/node/metadata?"+url.Values{"path": {"/目录"}}.Encode())
	var metadata struct {
		Children int32 `json:"children_count"`
		IsDir    bool  `json:"is_dir"`
	}
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if !metadata.IsDir || metadata.Children != 2 {
		t.Fatal(metadata)
	}
	w = request(a, "DELETE", "/api/clusters/1/nodes?path=/")
	if w.Code != 400 {
		t.Fatal("chroot root deletion must be rejected")
	}
	a.config.AllowDelete = false
	w = request(a, "DELETE", "/api/clusters/1/nodes?"+url.Values{"path": {"/目录"}}.Encode())
	if w.Code != 403 {
		t.Fatal("disabled deletion must be rejected")
	}
	a.config.AllowDelete = true
	w = request(a, "DELETE", "/api/clusters/1/nodes?"+url.Values{"path": {"/目录"}}.Encode())
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted_nodes":3`) {
		t.Fatal(w.Body.String())
	}
	exists, _, err := seed.Exists(root + "/目录")
	if err != nil || exists {
		t.Fatalf("recursive delete readback: exists=%v error=%v", exists, err)
	}
	exists, _, err = seed.Exists(root + "/empty")
	if err != nil || !exists {
		t.Fatalf("sibling must survive: exists=%v error=%v", exists, err)
	}
}
