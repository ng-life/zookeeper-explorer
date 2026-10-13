package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "serve")
	if err := os.WriteFile(file, []byte("[[clusters]]\nname='serve'\naddress='127.0.0.1:1'\n[[clusters]]\nname='中文 空格'\naddress='127.0.0.1:2'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
		ok   bool
	}{
		{[]string{"--config", file, "clusters", "--output=json"}, `["serve","中文 空格"]`, true},
		{[]string{"clusters", "-config=" + file}, "中文 空格", true},
		{[]string{"get", "/", "--config", file}, "", false},
		{[]string{"ls", "--unknown"}, "", false},
		{[]string{"get", "/", "extra"}, "", false},
		{[]string{"clusters", "--output=yaml"}, "", false},
		{[]string{"tui", "--help"}, "interactive", true},
		{[]string{"tui", "--output=json"}, "", false},
		{[]string{"tui"}, "", false},
		{[]string{"completion", "zsh"}, "compdef", true},
		{[]string{"completion", "bash"}, "complete", true},
		{[]string{"__complete", "--config", file, "ls", "--cluster", ""}, "中文 空格", true},
		{[]string{"__complete", "--config=" + file, "ls", "--cluster=ser"}, "serve", true},
		{[]string{"--help", "--config", "missing"}, "Usage:", true},
	} {
		var out, err bytes.Buffer
		code := Run(context.Background(), tc.args, strings.NewReader(""), &out, &err)
		if (code == 0) != tc.ok || !strings.Contains(out.String(), tc.want) {
			t.Errorf("%v: code=%d out=%q err=%q", tc.args, code, out.String(), err.String())
		}
	}
}
