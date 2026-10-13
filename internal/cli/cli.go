// Package cli provides one-shot ZooKeeper commands and live shell completion.
package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ng-life/zookeeper-explorer/internal/config"
	"github.com/ng-life/zookeeper-explorer/internal/server"
	"github.com/ng-life/zookeeper-explorer/internal/tui"
	"github.com/ng-life/zookeeper-explorer/internal/zookeeper"
	"github.com/spf13/cobra"
)

// Run keeps process termination in the entry point, after all sessions close.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	args = legacyFlags(args)
	var file, cluster, format string
	var timeout time.Duration
	var listen string
	var addresses []string
	var enable, disable bool
	root := &cobra.Command{Use: "zookeeper-explorer", Short: "Browse ZooKeeper nodes or run the Web server", SilenceUsage: true, SilenceErrors: true}
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	f := root.PersistentFlags()
	f.StringVarP(&file, "config", "c", config.DefaultPath(), "TOML configuration file")
	f.StringVar(&cluster, "cluster", "", "Cluster name (required with multiple clusters)")
	f.StringVar(&format, "output", "text", "Output: text or json")
	f.DurationVar(&timeout, "timeout", 10*time.Second, "Operation deadline")
	f.StringVar(&listen, "listen", "", "Web bind address")
	f.StringArrayVar(&addresses, "zk", nil, "Ensemble NAME=HOSTS (repeatable)")
	f.BoolVar(&enable, "allow-delete", false, "Enable Web recursive deletion")
	f.BoolVar(&disable, "disable-delete", false, "Disable Web recursive deletion")
	load := func() (config.Config, error) {
		a := []string{"--config", file}
		if listen != "" {
			a = append(a, "--listen", listen)
		}
		seen := map[string]bool{}
		completing := len(args) > 0 && args[0] == "__complete"
		for _, v := range addresses {
			// Cobra parses completion flags twice; array flags append twice.
			if completing && seen[v] {
				continue
			}
			seen[v] = true
			a = append(a, "--zk", v)
		}
		if enable {
			a = append(a, "--allow-delete")
		}
		if disable {
			a = append(a, "--disable-delete")
		}
		c, e := config.Load(a)
		if e != nil {
			return c, errors.New("configuration invalid or unreadable")
		}
		return c, nil
	}
	selectCluster := func(c config.Config) (config.Cluster, error) {
		var matches []config.Cluster
		for _, v := range c.Clusters {
			if v.Name == cluster || cluster == "" && len(c.Clusters) == 1 {
				matches = append(matches, v)
			}
		}
		if len(matches) != 1 {
			return config.Cluster{}, errors.New("select a unique cluster with --cluster")
		}
		return matches[0], nil
	}
	root.PersistentPreRunE = func(*cobra.Command, []string) error {
		if format != "text" && format != "json" {
			return errors.New("--output must be text or json")
		}
		if timeout <= 0 {
			return errors.New("--timeout must be positive")
		}
		return nil
	}
	serve := func(cmd *cobra.Command, a []string) error {
		c, e := load()
		if e != nil {
			return e
		}
		return server.Run(cmd.Context(), c)
	}
	root.Args = cobra.NoArgs
	root.RunE = serve
	root.AddCommand(&cobra.Command{Use: "serve", Short: "Run the Web server", Args: cobra.NoArgs, RunE: serve})
	root.AddCommand(&cobra.Command{Use: "clusters", Short: "List configured cluster names without connecting", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, a []string) error {
		c, e := load()
		if e != nil {
			return e
		}
		names := []string{}
		for _, v := range c.Clusters {
			names = append(names, v.Name)
		}
		if format == "json" {
			return json.NewEncoder(stdout).Encode(names)
		}
		for _, n := range names {
			fmt.Fprintln(stdout, terminal(n))
		}
		return nil
	}})
	completeClusters := func(cmd *cobra.Command, a []string, prefix string) ([]string, cobra.ShellCompDirective) {
		c, e := load()
		if e != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		out := []string{}
		for _, v := range c.Clusters {
			if strings.HasPrefix(v.Name, prefix) && safeCandidate(v.Name) {
				out = append(out, v.Name)
			}
		}
		sort.Strings(out)
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	root.RegisterFlagCompletionFunc("cluster", completeClusters)
	root.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json"}, cobra.ShellCompDirectiveNoFileComp
	})
	root.MarkPersistentFlagFilename("config", "toml")
	completePaths := func(cmd *cobra.Command, a []string, prefix string) ([]string, cobra.ShellCompDirective) {
		if len(a) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		c, e := load()
		if e != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cl, e := selectCluster(c)
		if e != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		parent := "/"
		if i := strings.LastIndex(prefix, "/"); i >= 0 {
			parent = prefix[:i+1]
		}
		parent, e = zookeeper.NormalizePath(parent)
		if e != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		deadline, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
		defer cancel()
		client, e := zookeeper.ConnectContext(deadline, cl.Address)
		if e != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		defer client.Close()
		names, e := childrenContext(deadline, client, parent)
		if e != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		out := []string{}
		for _, n := range names {
			candidate := path.Join(parent, n) + "/"
			if strings.HasPrefix(candidate, prefix) && safeCandidate(candidate) {
				out = append(out, candidate)
			}
		}
		sort.Strings(out)
		return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	}
	root.AddCommand(&cobra.Command{Use: "tui [PATH]", Short: "Open the interactive terminal browser", Args: cobra.MaximumNArgs(1), ValidArgsFunction: completePaths, RunE: func(cmd *cobra.Command, a []string) error {
		if format != "text" {
			return errors.New("tui does not support --output json; use ls, get, or stat")
		}
		c, e := load()
		if e != nil {
			return e
		}
		initial := "/"
		if len(a) > 0 {
			initial = a[0]
		}
		return tui.Run(cmd.Context(), c, cluster, initial, timeout, stdin, stdout)
	}})
	for _, action := range []string{"ls", "get", "stat"} {
		action := action
		cmd := &cobra.Command{Use: action + " PATH", Short: map[string]string{"ls": "List direct children", "get": "Read data (raw bytes in text mode)", "stat": "Read node metadata"}[action], Args: cobra.ExactArgs(1), ValidArgsFunction: completePaths}
		cmd.RunE = func(cmd *cobra.Command, a []string) error {
			p, e := zookeeper.NormalizePath(a[0])
			if e != nil {
				return e
			}
			c, e := load()
			if e != nil {
				return e
			}
			cl, e := selectCluster(c)
			if e != nil {
				return e
			}
			deadline, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			client, e := zookeeper.ConnectContext(deadline, cl.Address)
			if e != nil {
				return errors.New("ZooKeeper connection failed or timed out")
			}
			defer client.Close()
			// Closing the session interrupts pending requests when the deadline expires.
			done := make(chan struct{})
			defer close(done)
			go func() {
				select {
				case <-deadline.Done():
					client.Close()
				case <-done:
				}
			}()
			switch action {
			case "ls":
				names, _, e := client.Children(p)
				if e != nil {
					return errors.New("cannot list node (missing, denied, or unavailable)")
				}
				sort.Strings(names)
				if names == nil {
					names = []string{}
				}
				if format == "json" {
					return json.NewEncoder(stdout).Encode(names)
				}
				for _, n := range names {
					fmt.Fprintln(stdout, terminal(n))
				}
				return nil
			case "get":
				data, stat, e := client.Get(p)
				if e != nil {
					return errors.New("cannot read node (missing, denied, or unavailable)")
				}
				if format == "json" {
					return json.NewEncoder(stdout).Encode(struct {
						Path string      `json:"path"`
						Data string      `json:"data_base64"`
						Stat interface{} `json:"stat"`
					}{p, base64.StdEncoding.EncodeToString(data), stat})
				}
				_, e = stdout.Write(data)
				return e
			default:
				exists, stat, e := client.Exists(p)
				if e != nil {
					return errors.New("cannot inspect node (denied or unavailable)")
				}
				if !exists {
					return errors.New("node does not exist")
				}
				if format == "json" {
					return json.NewEncoder(stdout).Encode(stat)
				}
				b, e := json.MarshalIndent(stat, "", "  ")
				if e != nil {
					return e
				}
				_, e = fmt.Fprintln(stdout, string(b))
				return e
			}
		}
		root.AddCommand(cmd)
	}
	root.AddCommand(&cobra.Command{Use: "completion [zsh|bash]", Short: "Generate shell completion", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, a []string) error {
		switch a[0] {
		case "zsh":
			_, e := io.WriteString(stdout, zshCompletion)
			return e
		case "bash":
			_, e := io.WriteString(stdout, bashCompletion)
			return e
		default:
			return errors.New("completion supports zsh or bash")
		}
	}})
	root.SetArgs(args)
	if e := root.ExecuteContext(ctx); e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	return 0
}

func terminal(s string) string { q := strconv.Quote(s); return q[1 : len(q)-1] }
func safeCandidate(s string) bool {
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func childrenContext(ctx context.Context, c zookeeper.Client, p string) ([]string, error) {
	type result struct {
		names []string
		err   error
	}
	done := make(chan result, 1)
	go func() { n, _, e := c.Children(p); done <- result{n, e} }()
	select {
	case r := <-done:
		return r.names, r.err
	case <-ctx.Done():
		c.Close()
		return nil, ctx.Err()
	}
}

// Preserve the standard flag package's original single-dash long options.
func legacyFlags(args []string) []string {
	out := append([]string(nil), args...)
	values := map[string]bool{"config": true, "c": true, "listen": true, "zk": true, "cluster": true, "output": true, "timeout": true}
	bools := map[string]bool{"allow-delete": true, "disable-delete": true}
	for i := 0; i < len(out); i++ {
		if out[i] == "--" {
			break
		}
		name, _, equal := strings.Cut(strings.TrimLeft(out[i], "-"), "=")
		if !strings.HasPrefix(out[i], "-") {
			continue
		}
		if strings.HasPrefix(out[i], "-") && !strings.HasPrefix(out[i], "--") && name != "c" && (values[name] || bools[name]) {
			out[i] = "-" + out[i]
		}
		if values[name] && !equal {
			i++
		}
	}
	return out
}
