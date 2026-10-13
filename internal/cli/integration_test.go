package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-zookeeper/zk"
)

func TestCLIIntegration(t *testing.T) {
	address := os.Getenv("ZK_TEST_ADDRESS")
	if address == "" {
		t.Skip("requires isolated loopback ZooKeeper")
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
	root := fmt.Sprintf("/cli-test-%d", time.Now().UnixNano())
	if _, err = c.Create(root, nil, 0, zk.WorldACL(zk.PermAll)); err != nil {
		t.Fatal(err)
	}
	defer c.Delete(root, -1)
	name := "中文 空格"
	data := []byte{0, 255, 10}
	if _, err = c.Create(root+"/"+name, data, 0, zk.WorldACL(zk.PermAll)); err != nil {
		t.Fatal(err)
	}
	defer c.Delete(root+"/"+name, -1)
	run := func(args ...string) string {
		t.Helper()
		var out, stderr bytes.Buffer
		if args[0] == "__complete" {
			args = append([]string{"__complete", "--zk", "local=" + address + root}, args[1:]...)
		} else {
			args = append([]string{"--zk", "local=" + address + root}, args...)
		}
		if code := Run(context.Background(), args, nil, &out, &stderr); code != 0 {
			t.Fatalf("%v: %s", args, stderr.String())
		}
		return out.String()
	}
	if got := run("get", "/"+name); got != string(data) {
		t.Fatalf("raw bytes: %q", got)
	}
	var names []string
	if err = json.Unmarshal([]byte(run("ls", "/", "--output=json")), &names); err != nil || len(names) != 1 || names[0] != name {
		t.Fatalf("names: %v %v", names, err)
	}
	if got := run("__complete", "ls", "/"); !strings.Contains(got, "/"+name+"/") {
		t.Fatal(got)
	}
	if err = c.Delete(root+"/"+name, -1); err != nil {
		t.Fatal(err)
	}
	if got := run("__complete", "ls", "/"); strings.Contains(got, name) {
		t.Fatal("stale completion")
	}
	if got := run("ls", "/", "--output=json"); strings.TrimSpace(got) != "[]" {
		t.Fatal(got)
	}
}
