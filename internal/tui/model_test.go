package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/chensunlai/clash_mihomo/internal/manager"
	"github.com/muesli/termenv"
)

func modelForTest(t *testing.T) Model {
	t.Helper()
	t.Setenv("CLASHCTL_CORE", filepath.Join(t.TempDir(), "no-core"))
	m, err := manager.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(m)
}
func press(m Model, key string) (Model, tea.Cmd) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	switch key {
	case "enter":
		msg.Type = tea.KeyEnter
	case "esc":
		msg.Type = tea.KeyEsc
	case "tab":
		msg.Type = tea.KeyTab
	case "down":
		msg.Type = tea.KeyDown
	case "ctrl+s":
		msg.Type = tea.KeyCtrlS
	case "ctrl+a":
		msg.Type = tea.KeyCtrlA
	case " ":
		msg.Type = tea.KeySpace
	}
	updated, cmd := m.Update(msg)
	return updated.(Model), cmd
}
func TestSubscriptionFormAndPendingOperations(t *testing.T) {
	m := modelForTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "proxies: [{name: Node, type: socks5, server: 127.0.0.1, port: 1080}]\n")
	}))
	defer server.Close()
	m, _ = press(m, "a")
	if m.form == nil {
		t.Fatal("add did not open form")
	}
	m.form.fields[0].input.SetValue("Airport")
	m.form.fields[1].input.SetValue(server.URL + "?token=secret")
	if strings.Contains(ansi.Strip(m.View()), "?token=secret") {
		t.Fatal("form displayed subscription token")
	}
	m, cmd := press(m, "ctrl+s")
	if m.pending != 1 || m.form != nil || cmd == nil {
		t.Fatal("operation did not enter pending state")
	}
	updated, refresh := m.Update(cmd())
	m = updated.(Model)
	if m.failed || m.pending != 0 {
		t.Fatalf("add failed: %s", m.message)
	}
	updated, _ = m.Update(refresh())
	m = updated.(Model)
	if len(m.status.Subscriptions) != 1 {
		t.Fatal("subscription did not appear in UI")
	}
	m, _ = press(m, "n")
	if m.form == nil || m.form.kind != "create" {
		t.Fatal("cannot create instance")
	}
	m.form.fields[0].input.SetValue("work")
	m, cmd = press(m, "ctrl+s")
	updated, refresh = m.Update(cmd())
	m = updated.(Model)
	if m.failed {
		t.Fatal(m.message)
	}
	updated, _ = m.Update(refresh())
	m = updated.(Model)
	if len(m.status.Instances) != 1 {
		t.Fatal("instance was not created")
	}
	m, _ = press(m, "v")
	if m.form == nil || m.form.kind != "switch" {
		t.Fatal("switch subscription shortcut unavailable")
	}
}
func TestFlatWizardSupportsMultipleFilesAndCreatesFixedInstance(t *testing.T) {
	m := modelForTest(t)
	path := filepath.Join(t.TempDir(), "source.yaml")
	if err := os.WriteFile(path, []byte("proxies: [{name: One, type: socks5, server: 127.0.0.1, port: 1080}, {name: Two, type: http, server: 127.0.0.1, port: 8080}]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m, _ = press(m, "f")
	m.form.fields[0].input.SetValue("fixed")
	m, _ = press(m, "ctrl+s")
	m, _ = press(m, "l")
	if m.form == nil || m.form.kind != "flat-file" {
		t.Fatal("source file form unavailable")
	}
	m.form.fields[0].input.SetValue(path)
	m, cmd := press(m, "ctrl+s")
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if m.picker == nil {
		t.Fatal("node picker unavailable")
	}
	m, _ = press(m, " ")
	m, _ = press(m, "enter")
	if m.wizard == nil || len(m.wizard.selections) != 1 {
		t.Fatal("selection not added")
	}
	m, _ = press(m, "l")
	secondPath := filepath.Join(t.TempDir(), "other.yaml")
	content, _ := os.ReadFile(path)
	if err := os.WriteFile(secondPath, content, 0600); err != nil {
		t.Fatal(err)
	}
	m.form.fields[0].input.SetValue(secondPath)
	m, cmd = press(m, "ctrl+s")
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	m, _ = press(m, "down")
	m, _ = press(m, " ")
	m, _ = press(m, "enter")
	if len(m.wizard.selections) != 2 {
		t.Fatal("second file not added")
	}
	// Use an automatically found free range to avoid assumptions about the host.
	m.wizard.options.Port = 0
	m, cmd = press(m, "enter")
	updated, refresh := m.Update(cmd())
	m = updated.(Model)
	if m.failed {
		t.Fatal(m.message)
	}
	updated, _ = m.Update(refresh())
	m = updated.(Model)
	if len(m.status.Instances) != 1 || len(m.status.Instances[0].Entries) != 2 {
		t.Fatal("flat instance lost selections")
	}
	m, _ = press(m, "enter")
	if m.logs == nil || !strings.Contains(m.logs.View(), "s2") {
		t.Fatal("complete mapping viewer unavailable")
	}
}
func TestFlatWizardSelectsExistingSubscriptionsAndFreezesNodes(t *testing.T) {
	m := modelForTest(t)
	var requests atomic.Int32
	var changed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		names := []string{"One", "Shared"}
		if r.URL.Path == "/b" {
			names = []string{"Shared", "Other"}
		} else if changed.Load() {
			names = []string{"Replacement"}
		}
		fmt.Fprintln(w, "proxies:")
		for _, name := range names {
			fmt.Fprintf(w, "  - {name: %s, type: socks5, server: 127.0.0.1, port: 1080}\n", name)
		}
	}))
	defer server.Close()
	a, err := m.manager.AddSubscription("Team1", server.URL+"/a?token=hidden")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.manager.AddSubscription("Team2", server.URL+"/b")
	if err != nil {
		t.Fatal(err)
	}
	m.status.Subscriptions, _ = m.manager.Subscriptions()
	complete := func(cmd tea.Cmd) {
		t.Helper()
		if cmd == nil {
			t.Fatal("expected operation")
		}
		updated, _ := m.Update(cmd())
		m = updated.(Model)
		if m.failed {
			t.Fatal(m.message)
		}
	}
	m, _ = press(m, "f")
	m.form.fields[0].input.SetValue("fixed")
	m, _ = press(m, "ctrl+s")
	if m.form != nil || m.picker == nil || m.picker.kind != "flat-subscriptions" || len(m.picker.items) != 2 {
		t.Fatal("flat wizard did not offer existing subscriptions")
	}
	if strings.Contains(m.View(), "token=hidden") {
		t.Fatal("subscription picker exposed URL")
	}
	m, cmd := press(m, "enter")
	complete(cmd)
	if m.picker == nil || m.picker.kind != "flat-nodes" || len(m.picker.items) != 2 {
		t.Fatal("subscription node picker unavailable")
	}
	m, _ = press(m, " ")
	m, _ = press(m, "enter")
	m, _ = press(m, "a")
	m, _ = press(m, "down")
	m, cmd = press(m, "enter")
	complete(cmd)
	m.picker.filter.SetValue("Shared")
	m, _ = press(m, " ")
	m.picker.filter.SetValue("Other")
	m, _ = press(m, " ")
	m, _ = press(m, "enter")
	if len(m.wizard.selections) != 2 || len(m.wizard.selections[1].Names) != 2 {
		t.Fatal("lost selections across subscriptions or filters")
	}
	if view := m.View(); !strings.Contains(view, "Team1") || !strings.Contains(view, "Team2") {
		t.Fatal("port preview did not identify source subscriptions")
	}
	m, _ = press(m, "a")
	m, cmd = press(m, "enter")
	complete(cmd)
	if !m.picker.selected["One"] {
		t.Fatal("revisiting subscription lost its selection")
	}
	m, _ = press(m, "ctrl+a")
	m, _ = press(m, "enter")
	if len(m.wizard.selections) != 2 || len(m.wizard.selections[0].Names) != 2 {
		t.Fatal("revisiting subscription duplicated its nodes")
	}
	// Let the manager find a free contiguous range on the test host.
	m.wizard.options.Port = 0
	m, cmd = press(m, "enter")
	complete(cmd)
	i, err := m.manager.Instance("fixed")
	if err != nil || i.Kind != "flat" || i.Subscription != "" || len(i.Entries) != 4 {
		t.Fatalf("invalid flat snapshot: %+v (%v)", i, err)
	}
	for index, entry := range i.Entries {
		if entry.Port != i.Entries[0].Port+index {
			t.Fatal("node ports were not consecutive")
		}
	}
	if i.Entries[1].Node == i.Entries[2].Node {
		t.Fatal("same node name from different subscriptions collided")
	}
	if requests.Load() != 2 {
		t.Fatal("flat selection unnecessarily refreshed subscriptions")
	}
	path := filepath.Join(m.manager.Store.InstanceDir(i.ID), "config.yaml")
	before, _ := os.ReadFile(path)
	changed.Store(true)
	if err = m.manager.RefreshSubscription(a.ID); err != nil {
		t.Fatal(err)
	}
	if err = m.manager.DeleteSubscription(b.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("subscription refresh/deletion changed fixed snapshot")
	}
}
func TestScrollingAndNarrowTerminal(t *testing.T) {
	m := modelForTest(t)
	for n := 0; n < 50; n++ {
		m.status.Instances = append(m.status.Instances, manager.InstanceStatus{Instance: manager.Instance{ID: fmt.Sprint(n), Name: fmt.Sprintf("Instance %02d", n), Backend: "nohup", Kind: "subscription", Bind: "127.0.0.1"}})
	}
	m.width = 100
	m.height = 30
	m.cursor = 49
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Instance 49") {
		t.Fatal("selected row is not visible")
	}
	if lipgloss.Height(view) > 32 {
		t.Fatalf("view overflowed terminal: %d lines", lipgloss.Height(view))
	}
	m.width = 60
	if m.View() == "" {
		t.Fatal("narrow view is empty")
	}
}
func TestCancelledWizardIgnoresLateResult(t *testing.T) {
	m := modelForTest(t)
	updated, _ := m.Update(operationMsg{label: "load", value: localResult{path: "old", nodes: []manager.Node{{"name": "One", "type": "http"}}}})
	m = updated.(Model)
	if m.picker != nil {
		t.Fatal("late node result opened a cancelled wizard")
	}
	oldWizard := &flatWizard{}
	m.wizard = &flatWizard{labels: map[string]string{}}
	updated, _ = m.Update(operationMsg{label: "load", value: localResult{path: "old", nodes: []manager.Node{{"name": "One", "type": "http"}}, wizard: oldWizard}})
	m = updated.(Model)
	if m.picker != nil {
		t.Fatal("late node result from a cancelled wizard replaced a new wizard")
	}
}

func TestCommonTerminalFitsMainFormsAndPickers(t *testing.T) {
	m := modelForTest(t)
	m.width = 80
	m.height = 24
	check := func(label string) {
		t.Helper()
		if height := lipgloss.Height(m.View()); height > m.height {
			t.Fatalf("%s overflowed 80x24: %d lines", label, height)
		}
	}
	check("main")
	m.status.Subscriptions = []manager.Subscription{{ID: "a", Name: "Airport"}}
	m.createForm(false)
	check("create form")
	m.form = nil
	m.openPicker("flat-nodes", "选择节点", "", []pickItem{{"One", "One"}, {"Two", "Two"}})
	check("node picker")
	m.picker = nil
	m.wizard = &flatWizard{options: manager.CreateOptions{Name: "fixed", Port: 20000, Bind: "127.0.0.1", Backend: "nohup"}}
	check("wizard")
}
func TestPreview(t *testing.T) {
	path := os.Getenv("CLASHCTL_PREVIEW_PATH")
	if path == "" {
		return
	}
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := modelForTest(t)
	m.width = 116
	m.height = 36
	m.status.CoreInstalled = true
	m.page = 2
	m.status.Subscriptions = []manager.Subscription{{ID: "a", Name: "Airport A"}, {ID: "b", Name: "Airport B"}}
	m.status.Instances = []manager.InstanceStatus{
		{Instance: manager.Instance{ID: "work", Name: "work", Kind: "subscription", Backend: "user", Bind: "127.0.0.1", MixedPort: 17890, APIPort: 19090, Subscription: "a", Mode: "rule"}, Running: true},
		{Instance: manager.Instance{ID: "clone", Name: "work-2", Kind: "subscription", Backend: "nohup", Bind: "127.0.0.1", MixedPort: 17891, APIPort: 19091, Subscription: "a", Mode: "global"}, Running: true},
		{Instance: manager.Instance{ID: "fixed", Name: "fixed / parallel", Kind: "flat", Backend: "system", Bind: "127.0.0.1", APIPort: 19092, Autostart: true, Entries: []manager.Entry{{Node: "s1 · 东京", Original: "东京", Port: 20000}, {Node: "s1 · 新加坡", Original: "新加坡", Port: 20001}, {Node: "s2 · 洛杉矶", Original: "洛杉矶", Port: 20002}}}, Running: true},
	}
	m.message = "独立实例 · 连续 mixed 端口 · 固定节点快照"
	if err := os.WriteFile(path, []byte(m.View()), 0600); err != nil {
		t.Fatal(err)
	}
}
