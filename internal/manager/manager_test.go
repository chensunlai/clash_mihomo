package manager

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const fixture = `proxies:
  - {name: Alpha, type: socks5, server: 127.0.0.1, port: 1080}
  - {name: Beta, type: http, server: 127.0.0.1, port: 1081}
proxy-groups:
  - {name: PROXY, type: select, proxies: [Alpha, Beta]}
rules: [MATCH,PROXY]
`

func testManager(t *testing.T) *Manager {
	t.Helper()
	t.Setenv("CLASHCTL_CORE", filepath.Join(t.TempDir(), "no-core"))
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func writeFixture(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func testSubscription(t *testing.T, m *Manager) Subscription {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, fixture) }))
	t.Cleanup(server.Close)
	s, err := m.AddSubscription("main", server.URL+"?token=PRIVATE_SUBSCRIPTION")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestFlatIsFixedAndIgnoresOriginalConfiguration(t *testing.T) {
	m := testManager(t)
	dir := t.TempDir()
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); fmt.Fprint(w, fixture) }))
	defer remote.Close()
	source := writeFixture(t, filepath.Join(dir, "source.yaml"), fixture+fmt.Sprintf("tun: {enable: true}\ndns: {enable: true, listen: '0.0.0.0:53'}\nlisteners: [{type: mixed, port: 80}]\nproxy-providers:\n  remote: {type: http, url: '%s', interval: 1}\n", remote.URL))
	i, err := m.CreateFlat(CreateOptions{Name: "flat", Port: 0}, []Selection{{Path: source, Names: []string{"Alpha", "Beta"}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(m.Store.InstanceDir(i.ID), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := LoadYAML(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"tun", "dns", "proxy-providers", "rule-providers", "proxy-groups", "mixed-port"} {
		if config[field] != nil {
			t.Errorf("flat retained %s", field)
		}
	}
	listeners := config["listeners"].([]any)
	if len(listeners) != 2 {
		t.Fatal("wrong listener count")
	}
	for index, value := range listeners {
		listener := object(value)
		if listener["type"] != "mixed" || listener["proxy"] != i.Entries[index].Node || listener["port"] != i.Entries[index].Port {
			t.Fatal("listener routing changed")
		}
	}
	if i.Entries[1].Port != i.Entries[0].Port+1 {
		t.Fatal("ports are not contiguous")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	exported := filepath.Join(dir, "export.yaml")
	if err = m.ExportFlat(i.ID, exported); err != nil {
		t.Fatal(err)
	}
	output, _ := os.ReadFile(exported)
	if strings.Contains(string(output), "secret:") || strings.Contains(string(output), "external-controller") {
		t.Fatal("portable export leaked controller")
	}
	if requests.Load() != 0 {
		t.Fatal("flat import fetched a remote provider")
	}
	if _, err = m.Clone(i.ID, CreateOptions{Name: "flat-2"}); err != nil {
		t.Fatal(err)
	}
	if err = m.ExportFlat(i.ID, exported); err == nil {
		t.Fatal("overwrote export")
	}
}
func TestExtractionSupportsLocalProvidersDuplicatesAndDependencies(t *testing.T) {
	dir := t.TempDir()
	a := writeFixture(t, filepath.Join(dir, "a.yaml"), `proxies:
  - {name: Exit, type: http, server: 127.0.0.1, port: 8080, dialer-proxy: Hop}
  - {name: Hop, type: socks5, server: 127.0.0.1, port: 1080}
proxy-providers:
  local: {type: file, path: provider.yaml}
  inline: {type: inline, payload: [{name: Inline, type: socks5, server: 127.0.0.1, port: 1082}]}
`)
	writeFixture(t, filepath.Join(dir, "provider.yaml"), "proxies: [{name: Cached, type: http, server: 127.0.0.1, port: 8081}]\n")
	b := writeFixture(t, filepath.Join(dir, "b.yaml"), "proxies: [{name: Exit, type: socks5, server: 127.0.0.1, port: 1080}]\n")
	nodes, entries, err := ExtractNodes([]Selection{{a, []string{"Exit", "Inline", "Cached"}}, {b, []string{"Exit"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 5 || len(entries) != 4 {
		t.Fatalf("wrong dependency/entry count: %d/%d", len(nodes), len(entries))
	}
	if entries[0].Node == entries[3].Node {
		t.Fatal("cross-file names collided")
	}
	if nodes[1]["dialer-proxy"] != "s1 · Hop" {
		t.Fatal("dependency was not rewritten")
	}
	if _, _, err = ExtractNodes([]Selection{{a, []string{"OriginalGroup"}}}); err == nil {
		t.Fatal("accepted an unresolved policy group")
	}
	cycle := writeFixture(t, filepath.Join(dir, "cycle.yaml"), "proxies: [{name: A, type: http, dialer-proxy: B}, {name: B, type: http, dialer-proxy: A}]\n")
	if _, _, err = ExtractNodes([]Selection{{cycle, []string{"A"}}}); err == nil {
		t.Fatal("accepted a dependency cycle")
	}
}
func TestConcurrentClonesReserveIndependentPorts(t *testing.T) {
	m := testManager(t)
	s := testSubscription(t, m)
	original, err := m.CreateInstance(CreateOptions{Name: "original"}, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			other, err := New(m.Store.Root)
			if err != nil {
				errors <- err
				return
			}
			_, err = other.Clone(original.ID, CreateOptions{Name: fmt.Sprintf("clone-%d", n)})
			errors <- err
		}(n)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	items, err := m.Instances()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 13 {
		t.Fatalf("lost state updates: %d", len(items))
	}
	ports := map[int]bool{}
	for _, i := range items {
		for _, p := range i.Ports() {
			if ports[p] {
				t.Fatalf("port collision: %d", p)
			}
			ports[p] = true
		}
	}
	status, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(status)
	if strings.Contains(string(data), original.Secret) || strings.Contains(string(data), "PRIVATE_SUBSCRIPTION") {
		t.Fatal("status leaked private fields")
	}
	if err = m.DeleteSubscription(s.ID); err == nil {
		t.Fatal("deleted subscription in use")
	}
}
func TestOrdinaryModeKeepsRulesAndIsolatesListeners(t *testing.T) {
	source, err := LoadYAML([]byte(fixture + `mixed-port: 8080
port: 80
external-controller: 0.0.0.0:80
tun: {enable: true}
dns: {enable: true, listen: '127.0.0.1:53', nameserver: [1.1.1.1]}
listeners: [{type: mixed, port: 81}]
proxy-providers:
  remote: {type: http, url: 'https://example.invalid/p.yaml', path: '/shared/provider.yaml'}
`))
	if err != nil {
		t.Fatal(err)
	}
	i := Instance{Bind: "127.0.0.1", Mode: "rule", MixedPort: 17890, APIPort: 19090, Secret: "private"}
	config, err := OrdinaryConfig(source, i)
	if err != nil {
		t.Fatal(err)
	}
	if config["mixed-port"] != 17890 || config["external-controller"] != "127.0.0.1:19090" {
		t.Fatal("subscription overrode instance listeners")
	}
	for _, key := range []string{"listeners", "tun", "port"} {
		if config[key] != nil {
			t.Fatalf("retained %s", key)
		}
	}
	if object(config["dns"])["listen"] != nil {
		t.Fatal("retained DNS listener")
	}
	if config["rules"] == nil || config["proxy-groups"] == nil {
		t.Fatal("discarded ordinary policy")
	}
	if object(object(config["proxy-providers"])["remote"])["path"] == "/shared/provider.yaml" {
		t.Fatal("shared provider path")
	}
	if source["mixed-port"] != 8080 {
		t.Fatal("mutated source config")
	}
}
func TestInvalidUpdatesKeepOldConfiguration(t *testing.T) {
	m := testManager(t)
	var broken atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() {
			fmt.Fprint(w, "not a clash config")
		} else {
			fmt.Fprint(w, fixture)
		}
	}))
	defer server.Close()
	s, err := m.AddSubscription("main", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	i, err := m.CreateInstance(CreateOptions{Name: "work"}, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.Store.InstanceDir(i.ID), "config.yaml")
	before, _ := os.ReadFile(path)
	broken.Store(true)
	if err = m.RefreshSubscription(s.ID); err == nil {
		t.Fatal("accepted invalid subscription")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("replaced working configuration")
	}
	state, _ := m.Store.Read()
	if len(state.Subscriptions) != 1 {
		t.Fatal("state changed on failure")
	}
}
func TestConflictAndFailedCreationDoNotLeakReservations(t *testing.T) {
	m := testManager(t)
	s := testSubscription(t, m)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if _, err = m.CreateInstance(CreateOptions{Name: "conflict", Port: port}, s.ID); err == nil {
		t.Fatal("accepted an occupied TCP port")
	}
	items, _ := m.Instances()
	if len(items) != 0 {
		t.Fatal("leaked failed reservation")
	}
	i, err := m.CreateInstance(CreateOptions{Name: "good"}, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.CreateInstance(CreateOptions{Name: "conflict", Port: i.APIPort}, s.ID); err == nil {
		t.Fatal("accepted reserved API port")
	}
}
func TestResourceLocksPermitParallelIndependentUpdates(t *testing.T) {
	m := testManager(t)
	var gate atomic.Bool
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gate.Load() {
			entered <- struct{}{}
			<-release
		}
		fmt.Fprint(w, fixture)
	}))
	defer server.Close()
	a, err := m.AddSubscription("a", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.AddSubscription("b", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	gate.Store(true)
	errors := make(chan error, 2)
	go func() { errors <- m.RefreshSubscription(a.ID) }()
	go func() { errors <- m.RefreshSubscription(b.ID) }()
	defer close(release)
	for n := 0; n < 2; n++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("independent network operations were serialized")
		}
	}
	// Unblock both requests before waiting for completion.
	release <- struct{}{}
	release <- struct{}{}
	for n := 0; n < 2; n++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
}

func TestCanonicalHomeAndConcurrentExports(t *testing.T) {
	m := testManager(t)
	alias := filepath.Join(t.TempDir(), "linked-home")
	if err := os.Symlink(m.Store.Root, alias); err != nil {
		t.Fatal(err)
	}
	other, err := New(alias)
	if err != nil {
		t.Fatal(err)
	}
	if other.Store.Root != m.Store.Root {
		t.Fatal("symlinked home used a different instance namespace")
	}
	source := writeFixture(t, filepath.Join(t.TempDir(), "source.yaml"), fixture)
	a, err := m.CreateFlat(CreateOptions{Name: "a"}, []Selection{{source, []string{"Alpha"}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.CreateFlat(CreateOptions{Name: "b"}, []Selection{{source, []string{"Beta"}}})
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "fixed.yaml")
	errors := make(chan error, 2)
	go func() { errors <- m.ExportFlat(a.ID, output) }()
	go func() { errors <- other.ExportFlat(b.ID, output) }()
	succeeded := 0
	for n := 0; n < 2; n++ {
		if <-errors == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected one exclusive export, got %d", succeeded)
	}
}
