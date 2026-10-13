// Package tui implements a read-only terminal browser for ZooKeeper.
package tui

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/go-zookeeper/zk"
	"github.com/ng-life/zookeeper-explorer/internal/config"
	"github.com/ng-life/zookeeper-explorer/internal/zookeeper"
	"golang.org/x/term"
)

type item struct{ name, node string }

func (i item) Title() string       { return singleLine(i.name) }
func (i item) Description() string { return singleLine(i.node) }
func (i item) FilterValue() string { return i.name }

type request struct {
	address, node string
	browse        bool
	id            uint64
}
type result struct {
	request
	children []string
	data     []byte
	stat     *zk.Stat
	err      error
}
type reader func(context.Context, request) result

type model struct {
	ctx                                  context.Context
	clusters                             []config.Cluster
	cluster                              int
	choosing, details, metadata, loading bool
	current, shown, status               string
	nodes                                list.Model
	view                                 viewport.Model
	spin                                 spinner.Model
	data                                 []byte
	stat                                 *zk.Stat
	width, height                        int
	generation                           uint64
	cancel                               context.CancelFunc
	timeout                              time.Duration
	read                                 reader
}

// Run requires a real terminal; existing CLI commands remain usable in pipelines.
func Run(ctx context.Context, c config.Config, selected, initial string, timeout time.Duration, in io.Reader, out io.Writer) error {
	input, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(input.Fd())) {
		return errors.New("tui requires an interactive terminal")
	}
	output, ok := out.(*os.File)
	if !ok || !term.IsTerminal(int(output.Fd())) {
		return errors.New("tui requires terminal output")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m, err := newModel(ctx, c, selected, initial, timeout, readNode)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func newModel(ctx context.Context, c config.Config, selected, initial string, timeout time.Duration, read reader) (*model, error) {
	p, err := zookeeper.NormalizePath(initial)
	if err != nil {
		return nil, err
	}
	if len(c.Clusters) == 0 {
		return nil, errors.New("no clusters configured; use --config or --zk")
	}
	seen := map[string]bool{}
	index := -1
	for i, cl := range c.Clusters {
		if cl.Name == "" || seen[cl.Name] {
			return nil, errors.New("cluster names must be nonempty and unique")
		}
		seen[cl.Name] = true
		if cl.Name == selected {
			index = i
		}
	}
	if selected != "" && index < 0 {
		return nil, errors.New("unknown cluster; run clusters to list names")
	}
	m := &model{ctx: ctx, clusters: c.Clusters, cluster: index, current: p, timeout: timeout, read: read, width: 100, height: 30}
	m.nodes = list.New(nil, list.NewDefaultDelegate(), 30, 20)
	m.nodes.SetShowHelp(false)
	m.nodes.DisableQuitKeybindings()
	m.nodes.SetShowStatusBar(true)
	m.nodes.SetFilteringEnabled(true)
	m.nodes.SetShowTitle(false)
	m.view = viewport.New(60, 20)
	m.spin = spinner.New()
	m.spin.Spinner = spinner.Dot
	if index < 0 && len(c.Clusters) == 1 {
		m.cluster = 0
	}
	if m.cluster < 0 {
		m.choose()
	} else {
		m.status = "Ready"
	}
	m.resize()
	return m, nil
}
func (m *model) Init() tea.Cmd {
	if m.choosing {
		return nil
	}
	return m.fetch(m.current, true)
}
func (m *model) choose() {
	if m.cancel != nil {
		m.cancel()
	}
	m.generation++
	m.loading = false
	m.choosing = true
	m.details = false
	items := make([]list.Item, 0, len(m.clusters))
	for _, cl := range m.clusters {
		items = append(items, item{cl.Name, ""})
	}
	m.nodes.SetItems(items)
	m.nodes.ResetFilter()
	m.nodes.Select(0)
	m.status = "Select a cluster and press Enter"
}
func (m *model) fetch(node string, browse bool) tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	m.generation++
	m.loading = true
	m.shown = node
	m.data = nil
	m.stat = nil
	m.view.SetContent("Loading…")
	m.view.GotoTop()
	id := m.generation
	ctx, cancel := context.WithTimeout(m.ctx, m.timeout)
	m.cancel = cancel
	req := request{address: m.clusters[m.cluster].Address, node: node, browse: browse, id: id}
	return tea.Batch(m.spin.Tick, func() tea.Msg { defer cancel(); return m.read(ctx, req) })
}
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		return m, nil
	case result:
		if msg.id != m.generation {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.status = "Read failed: node missing, denied, or connection unavailable (r retries)"
			m.view.SetContent(m.status)
			return m, nil
		}
		if msg.browse {
			m.current = msg.node
			items := []list.Item{}
			if m.current != "/" {
				items = append(items, item{"..", path.Dir(m.current)})
			}
			sort.Strings(msg.children)
			for _, name := range msg.children {
				items = append(items, item{name, path.Join(m.current, name)})
			}
			m.nodes.ResetFilter()
			m.nodes.SetItems(items)
			m.nodes.Select(0)
		}
		m.shown = msg.node
		m.data = msg.data
		m.stat = msg.stat
		m.status = "Ready"
		m.renderDetail()
		return m, nil
	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if !m.details && (msg.String() == "esc" || msg.String() == "ctrl+g") {
			m.nodes.ResetFilter()
			return m, nil
		}
		// While the list filter is being edited, letters belong to the filter.
		filtering := !m.details && m.nodes.FilterState() == list.Filtering
		if !filtering {
			switch msg.String() {
			case "q":
				return m, tea.Quit
			case "c":
				m.current = "/"
				m.choose()
				return m, nil
			case "tab":
				if !m.choosing {
					m.details = !m.details
				}
				return m, nil
			case "m":
				m.metadata = !m.metadata
				m.renderDetail()
				return m, nil
			case "r":
				if !m.choosing {
					return m, m.fetch(m.current, true)
				}
			case "backspace", "left":
				if !m.choosing && !m.details {
					return m, m.fetch(path.Dir(m.current), true)
				}
			case "enter", "right":
				if !m.details {
					if it, ok := m.nodes.SelectedItem().(item); ok {
						if m.choosing {
							for i, cl := range m.clusters {
								if cl.Name == it.name {
									m.cluster = i
									break
								}
							}
							m.choosing = false
							m.nodes.SetItems(nil)
							return m, m.fetch(m.current, true)
						}
						return m, m.fetch(it.node, true)
					}
				}
			}
		}
	}
	if m.details {
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd
	}
	before := m.nodes.SelectedItem()
	var cmd tea.Cmd
	m.nodes, cmd = m.nodes.Update(msg)
	if !m.choosing && m.nodes.SelectedItem() != before {
		if it, ok := m.nodes.SelectedItem().(item); ok && it.name != ".." {
			return m, tea.Batch(cmd, m.fetch(it.node, false))
		}
	}
	return m, cmd
}

func (m *model) resize() {
	w := max(20, m.width)
	h := max(8, m.height-8)
	if w < 90 {
		m.nodes.SetSize(w-4, max(3, h/2))
		m.view.Width = w - 4
		m.view.Height = max(3, h-h/2-2)
	} else {
		left := max(24, w/3)
		m.nodes.SetSize(left-4, h-2)
		m.view.Width = w - left - 4
		m.view.Height = h - 2
	}
	m.renderDetail()
}
func (m *model) renderDetail() {
	if m.loading {
		return
	}
	if m.stat == nil {
		m.view.SetContent("Select a node to inspect its data.")
		return
	}
	var content string
	if m.metadata {
		b, _ := json.MarshalIndent(m.stat, "", "  ")
		content = string(b)
	} else if len(m.data) == 0 {
		content = "(empty node data)"
	} else {
		isText := utf8.Valid(m.data) && !bytes.ContainsRune(m.data, 0)
		data := m.data
		truncated := len(data) > 64*1024
		if truncated {
			data = data[:64*1024]
		}
		if isText {
			for !utf8.Valid(data) {
				data = data[:len(data)-1]
			}
			var b bytes.Buffer
			if json.Indent(&b, data, "", "  ") == nil {
				data = b.Bytes()
			}
			content = safeText(string(data))
		} else {
			if len(data) > 4096 {
				data = data[:4096]
				truncated = true
			}
			content = "Binary data (hex)\n" + hex.Dump(data)
		}
		if truncated {
			content += "\n[Preview truncated; use get PATH to read the complete data]"
		}
	}
	content = lipgloss.NewStyle().Width(max(1, m.view.Width)).Render(content)
	m.view.SetContent(content)
}
func (m *model) View() string {
	if m.width < 60 || m.height < 16 {
		return "ZooKeeper Explorer\nResize terminal to at least 60×16.\nq / Ctrl+C quit"
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	cluster := "Choose cluster"
	if !m.choosing {
		cluster = singleLine(m.clusters[m.cluster].Name) + "  " + safeText(m.current)
	}
	heading := title.Width(m.width).Render(ansi.Truncate("ZooKeeper Explorer  ·  "+cluster, m.width, "…"))
	leftTitle := "Nodes  (/ filter)"
	if m.choosing {
		leftTitle = "Clusters  (/ filter)"
	}
	rightTitle := "Data"
	if m.metadata {
		rightTitle = "Metadata"
	}
	rightTitle += "  " + safeText(m.shown)
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))
	left := border.Width(m.nodes.Width()).Render(title.Render(leftTitle) + "\n" + m.nodes.View())
	right := border.Width(m.view.Width).Render(title.Render(ansi.Truncate(rightTitle, m.view.Width, "…")) + "\n" + m.view.View())
	if m.details {
		right = border.BorderForeground(lipgloss.Color("81")).Width(m.view.Width).Render(title.Render(ansi.Truncate(rightTitle, m.view.Width, "…")) + "\n" + m.view.View())
	}
	body := left
	if !m.choosing {
		if m.width < 90 {
			body = lipgloss.JoinVertical(lipgloss.Left, left, right)
		} else {
			body = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
		}
	}
	status := m.status
	if m.loading {
		status = m.spin.View() + " Loading " + safeText(m.shown)
	}
	help := "↑↓ navigate · Enter open · ← parent · Tab pane · m metadata · r refresh · c clusters · q quit"
	if m.width < 90 {
		help = "↑↓ move · Enter open · ← back · / filter · Tab pane\nm metadata · r refresh · c clusters · q quit"
	}
	footer := lipgloss.NewStyle().Width(m.width).Render(ansi.Truncate(status, m.width, "…") + "\n" + help)
	return heading + "\n" + body + "\n" + footer
}

// readNode owns a single bounded session so canceled UI requests cannot leak it.
func readNode(ctx context.Context, req request) result {
	out := result{request: req}
	client, err := zookeeper.ConnectContext(ctx, req.address)
	if err != nil {
		out.err = err
		return out
	}
	defer client.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			client.Close()
		case <-done:
		}
	}()
	if req.browse {
		out.children, _, err = client.Children(req.node)
		if err != nil {
			out.err = err
			return out
		}
	}
	out.data, out.stat, out.err = client.Get(req.node)
	return out
}

// Keep newlines and tabs useful while escaping terminal control sequences.
func safeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var _ tea.Model = (*model)(nil)

func singleLine(s string) string { q := strconv.Quote(s); return q[1 : len(q)-1] }
