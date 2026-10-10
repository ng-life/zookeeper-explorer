package server

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-zookeeper/zk"
	"github.com/ng-life/zookeeper-explorer/internal/config"
	"github.com/ng-life/zookeeper-explorer/internal/zookeeper"
)

type fakeClient struct {
	data    []byte
	tree    map[string][]string
	deleted []string
	fail    string
}

func (f *fakeClient) Children(p string) ([]string, *zk.Stat, error) {
	children, ok := f.tree[p]
	if !ok {
		return nil, nil, zk.ErrNoNode
	}
	return append([]string{}, children...), &zk.Stat{}, nil
}
func (f *fakeClient) Get(p string) ([]byte, *zk.Stat, error) {
	return f.data, &zk.Stat{DataLength: int32(len(f.data))}, nil
}
func (f *fakeClient) Exists(p string) (bool, *zk.Stat, error) {
	_, ok := f.tree[p]
	return ok, &zk.Stat{DataLength: int32(len(f.data)), NumChildren: int32(len(f.tree[p]))}, nil
}
func (f *fakeClient) Delete(p string, _ int32) error {
	if p == f.fail {
		return errors.New("concurrent child created")
	}
	f.deleted = append(f.deleted, p)
	return nil
}
func (f *fakeClient) Close() {}
func testApp(f *fakeClient, enable bool) *Server {
	a := New(config.Config{Clusters: []config.Cluster{{Name: "test", Address: "localhost:2181"}}, AllowDelete: enable})
	a.dial = func(string) (zookeeper.Client, error) { return f, nil }
	return a
}
func request(a *Server, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}
func TestNodesAndDownload(t *testing.T) {
	for _, data := range [][]byte{[]byte("你好"), {0xff, 0x00}, {}} {
		f := &fakeClient{data: data, tree: map[string][]string{"/node": {"z", "A"}}}
		a := testApp(f, false)
		w := request(a, "GET", "/api/clusters/0/nodes?path=/node/")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var out nodeResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Path != "/node" || out.Nodes[0].Name != "A" || out.DataLen != int32(len(data)) {
			t.Fatalf("unexpected response: %+v", out)
		}
		if len(data) > 0 && data[0] == 0xff {
			if out.Data != nil || out.DataBase64 == nil || *out.DataBase64 != "/wA=" {
				t.Fatal(out)
			}
		} else if out.Data == nil || *out.Data != string(data) {
			t.Fatal(out)
		}
		w = request(a, "GET", "/api/clusters/0/node/download?path=/node")
		if w.Code != 200 || !reflect.DeepEqual(w.Body.Bytes(), data) && len(data) > 0 {
			t.Fatal(w)
		}
		if !strings.Contains(w.Header().Get("Content-Disposition"), "filename*=UTF-8''node") {
			t.Fatal(w.Header())
		}
	}
}
func TestGuardsAndMetadata(t *testing.T) {
	f := &fakeClient{tree: map[string][]string{"/": {}, "/node": {"a"}}}
	a := testApp(f, false)
	for _, tc := range []struct {
		method, target string
		status         int
	}{{"GET", "/", 200}, {"GET", "/api/settings", 200}, {"GET", "/api/clusters", 200}, {"GET", "/api/clusters/0/node/metadata?path=/node", 200}, {"GET", "/api/clusters/0/node/metadata?path=/missing", 404}, {"GET", "/api/clusters/2/nodes", 404}, {"GET", "/api/clusters/0/nodes?path=/a/../b", 400}, {"GET", "/api/clusters/0/node/download", 400}, {"DELETE", "/api/clusters/0/nodes?path=/node", 403}} {
		w := request(a, tc.method, tc.target)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.target, w.Code, w.Body.String())
		}
	}
	a.config.AllowDelete = true
	for _, p := range []string{"/", "/a/..", "//"} {
		w := request(a, "DELETE", "/api/clusters/0/nodes?path="+p)
		if w.Code != 400 {
			t.Fatal(w)
		}
	}
	if len(f.deleted) != 0 {
		t.Fatal(f.deleted)
	}
}
func TestRecursiveDelete(t *testing.T) {
	for _, fail := range []string{"", "/node"} {
		f := &fakeClient{tree: map[string][]string{"/node": {"child"}, "/node/child": {"leaf"}, "/node/child/leaf": {}}, fail: fail}
		w := request(testApp(f, true), "DELETE", "/api/clusters/0/nodes?path=/node")
		want := []string{"/node/child/leaf", "/node/child"}
		if fail == "" {
			want = append(want, "/node")
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted_nodes":3`) {
				t.Fatal(w)
			}
		} else if w.Code != 409 || !strings.Contains(w.Body.String(), "2 child node(s) already deleted") {
			t.Fatal(w)
		}
		if !reflect.DeepEqual(f.deleted, want) {
			t.Fatal(f.deleted)
		}
	}
}
