package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOverrides(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(file, []byte("listen='127.0.0.1:8090'\nallow_delete=true\n[[clusters]]\nname='test'\naddress='host:2181/app'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load([]string{"--config", file, "--disable-delete", "--zk", "new=a:2181,b:2181/root", "--listen", "127.0.0.1:9090"})
	if err != nil || c.AllowDelete || c.Listen != "127.0.0.1:9090" || len(c.Clusters) != 1 || c.Clusters[0].Name != "new" {
		t.Fatal(c, err)
	}
	if _, err := Load([]string{"--allow-delete", "--disable-delete"}); err == nil {
		t.Fatal("expected conflict")
	}
}
