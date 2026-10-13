package tui

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/go-zookeeper/zk"
)

func TestReadNodeIntegration(t *testing.T) {
	address := os.Getenv("ZK_TEST_ADDRESS")
	if address == "" {
		t.Skip("requires isolated ZooKeeper")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("loopback required")
	}
	c, events, err := zk.Connect([]string{address}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go func() {
		for range events {
		}
	}()
	root := fmt.Sprintf("/tui-test-%d", time.Now().UnixNano())
	if _, err = c.Create(root, []byte("你好"), 0, zk.WorldACL(zk.PermAll)); err != nil {
		t.Fatal(err)
	}
	defer c.Delete(root, -1)
	data := []byte{0, 255, 27}
	name := "中文 空格"
	if _, err = c.Create(root+"/"+name, data, 0, zk.WorldACL(zk.PermAll)); err != nil {
		t.Fatal(err)
	}
	defer c.Delete(root+"/"+name, -1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req := request{address: address + root, node: "/", browse: true, id: 3}
	got := readNode(ctx, req)
	if got.err != nil || len(got.children) != 1 || got.children[0] != name || string(got.data) != "你好" || got.stat == nil {
		t.Fatalf("%+v", got)
	}
	req.node = "/" + name
	req.browse = false
	got = readNode(ctx, req)
	if got.err != nil || !bytes.Equal(got.data, data) {
		t.Fatalf("%+v", got)
	}
	req.node = "/missing"
	if got = readNode(ctx, req); got.err == nil {
		t.Fatal("missing node should fail")
	}
	expired, stop := context.WithCancel(context.Background())
	stop()
	start := time.Now()
	got = readNode(expired, req)
	if got.err == nil || time.Since(start) > time.Second {
		t.Fatal("cancellation")
	}
}
