package zookeeper

import (
	"testing"
)

func TestChrootPaths(t *testing.T) {
	servers, root, err := ParseAddress("a:2181,b:2181/root")
	if err != nil || len(servers) != 2 || root != "/root" {
		t.Fatal(servers, root, err)
	}
	client := rootedClient{root: root}
	if client.path("/") != "/root" || client.path("/child") != "/root/child" {
		t.Fatal("chroot translation")
	}
}
