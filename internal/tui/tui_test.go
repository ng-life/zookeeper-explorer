package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/go-zookeeper/zk"
	"github.com/ng-life/zookeeper-explorer/internal/config"
)

func testModel(t *testing.T, selected string) *model {
	t.Helper()
	m, e := newModel(context.Background(), config.Config{Clusters: []config.Cluster{{Name: "one", Address: "127.0.0.1:1"}, {Name: "two", Address: "127.0.0.1:2"}}}, selected, "/", time.Second, func(ctx context.Context, r request) result { return result{request: r} })
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if m.cancel != nil {
			m.cancel()
		}
	})
	return m
}
func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func TestClusterPickerAndStaleResult(t *testing.T) {
	m := testModel(t, "")
	if !m.choosing || m.Init() != nil {
		t.Fatal("multi-cluster startup should remain local")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.choosing || m.cluster != 0 || !m.loading {
		t.Fatal("cluster selection")
	}
	old := m.generation
	m.Update(key("c"))
	m.Update(result{request: request{id: old, node: "/stale", browse: true}, data: []byte("wrong")})
	if m.current != "/" || !m.choosing || len(m.data) != 0 {
		t.Fatal("stale result applied")
	}
}
func TestNavigationAndDetails(t *testing.T) {
	m := testModel(t, "one")
	m.Init()
	m.Update(result{request: request{id: m.generation, node: "/", browse: true}, children: []string{"配置", "a"}, stat: &zk.Stat{Version: 9}})
	if m.nodes.Items()[0].(item).name != "a" {
		t.Fatal("unsorted children")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.shown != "/配置" || !m.loading {
		t.Fatal("selection preview")
	}
	m.Update(result{request: request{id: m.generation, node: "/配置"}, data: []byte("{\"hello\":\"你好\"}"), stat: &zk.Stat{Version: 9}})
	if !strings.Contains(m.view.View(), "你好") {
		t.Fatal(m.view.View())
	}
	m.Update(key("m"))
	if !strings.Contains(m.view.View(), "Version") {
		t.Fatal("metadata")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.shown != "/配置" {
		t.Fatal("open")
	}
	m.Update(result{request: request{id: m.generation, node: "/配置", browse: true}, stat: &zk.Stat{}})
	if len(m.nodes.Items()) != 1 || m.nodes.Items()[0].(item).name != ".." {
		t.Fatal("leaf parent")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.shown != "/" {
		t.Fatal("parent")
	}
}
func TestErrorFilteringAndLayout(t *testing.T) {
	m := testModel(t, "one")
	m.Init()
	m.Update(result{request: request{id: m.generation}, err: errors.New("secret address")})
	if strings.Contains(m.View(), "secret address") || m.loading {
		t.Fatal("error leaking or spinner stuck")
	}
	m.nodes.SetItems([]list.Item{item{"qqq", "/qqq"}})
	m.Update(key("/"))
	_, cmd := m.Update(key("q"))
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("q quit while filtering")
		}
	}
	m.data = []byte{0, 255, 27}
	m.stat = &zk.Stat{}
	m.renderDetail()
	if !strings.Contains(m.view.View(), "Binary") {
		t.Fatal("binary preview")
	}
	if strings.Contains(safeText("hi\x1b[31m\r"), "\x1b") {
		t.Fatal("terminal escape")
	}
	for _, width := range []int{60, 80, 100, 140} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		if len(strings.Split(m.View(), "\n")) > 30 {
			t.Fatalf("layout exceeds height at %d", width)
		}
		for _, line := range strings.Split(m.View(), "\n") {
			if lipgloss.Width(line) > width {
				t.Fatalf("width %d exceeded: %q", width, line)
			}
		}
	}
}
