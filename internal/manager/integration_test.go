package manager

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func localProxy(t *testing.T, label string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			fmt.Fprint(w, label)
			return
		}
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err = fmt.Fprint(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			return
		}
		request, err := http.ReadRequest(buffer.Reader)
		if err != nil {
			return
		}
		request.Body.Close()
		fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(label), label)
	}))
	t.Cleanup(server.Close)
	return server
}
func coreFixture(t *testing.T) (*Manager, string, string) {
	t.Helper()
	binary := os.Getenv("CLASHCTL_TEST_CORE")
	if binary == "" {
		t.Skip("set CLASHCTL_TEST_CORE for real mihomo integration")
	}
	t.Setenv("CLASHCTL_CORE", binary)
	m, err := New(filepath.Join(t.TempDir(), "manager with spaces"))
	if err != nil {
		t.Fatal(err)
	}
	a, b := localProxy(t, "Alpha"), localProxy(t, "Beta")
	portA := a.Listener.Addr().(*net.TCPAddr).Port
	portB := b.Listener.Addr().(*net.TCPAddr).Port
	config := fmt.Sprintf("proxies:\n  - {name: Alpha, type: http, server: 127.0.0.1, port: %d}\n  - {name: Beta, type: http, server: 127.0.0.1, port: %d}\nproxy-groups:\n  - {name: PROXY, type: select, proxies: [Alpha, Beta]}\nrules: ['MATCH,PROXY']\n", portA, portB)
	path := writeFixture(t, filepath.Join(t.TempDir(), "source.yaml"), config)
	return m, config, path
}
func requestHTTP(t *testing.T, port int, want string) {
	t.Helper()
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get("http://destination.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != want {
		t.Fatalf("proxy %d returned %q, want %q (%v)", port, body, want, err)
	}
}
func requestSOCKS(t *testing.T, port int, want string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write([]byte{5, 1, 0})
	reply := make([]byte, 2)
	if _, err = io.ReadFull(conn, reply); err != nil || reply[1] != 0 {
		t.Fatalf("SOCKS greeting: %v %v", reply, err)
	}
	host := "destination.invalid"
	request := append([]byte{5, 1, 0, 3, byte(len(host))}, []byte(host)...)
	request = binary.BigEndian.AppendUint16(request, 80)
	_, _ = conn.Write(request)
	response := make([]byte, 4)
	if _, err = io.ReadFull(conn, response); err != nil || response[1] != 0 {
		t.Fatalf("SOCKS CONNECT: %v %v", response, err)
	}
	length := 0
	switch response[3] {
	case 1:
		length = 4
	case 4:
		length = 16
	case 3:
		b := make([]byte, 1)
		_, _ = io.ReadFull(conn, b)
		length = int(b[0])
	default:
		t.Fatal("invalid SOCKS address type")
	}
	_, _ = io.CopyN(io.Discard, conn, int64(length+2))
	_, _ = fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: destination.invalid\r\nConnection: close\r\n\r\n")
	result, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil || string(body) != want {
		t.Fatalf("SOCKS returned %q, want %q (%v)", body, want, err)
	}
}
func TestRealCoreConcurrentSubscriptionsAndFlatRouting(t *testing.T) {
	m, config, path := coreFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, config) }))
	defer server.Close()
	s, err := m.AddSubscription("main", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.CreateInstance(CreateOptions{Name: "one"}, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Clone(first.ID, CreateOptions{Name: "two"})
	if err != nil {
		t.Fatal(err)
	}
	flat, err := m.CreateFlat(CreateOptions{Name: "fixed"}, []Selection{{path, []string{"Alpha", "Beta"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []Instance{first, second, flat} {
		t.Cleanup(func() { _ = m.DeleteInstance(i.ID) })
		if err = m.Start(i.ID); err != nil {
			t.Fatal(err)
		}
	}
	pid := m.OwnedPID(first)
	if err = m.Start(first.ID); err != nil || m.OwnedPID(first) != pid {
		t.Fatal("start is not idempotent")
	}
	if err = m.SelectNode(second.ID, "PROXY", "Beta"); err != nil {
		t.Fatal(err)
	}
	requestHTTP(t, first.MixedPort, "Alpha")
	requestHTTP(t, second.MixedPort, "Beta")
	requestHTTP(t, flat.Entries[0].Port, "Alpha")
	requestHTTP(t, flat.Entries[1].Port, "Beta")
	requestSOCKS(t, flat.Entries[0].Port, "Alpha")
	requestSOCKS(t, flat.Entries[1].Port, "Beta")
	flatPath := filepath.Join(m.Store.InstanceDir(flat.ID), "config.yaml")
	before, _ := os.ReadFile(flatPath)
	if err = m.RefreshSubscription(s.ID); err != nil {
		t.Fatal(err)
	}
	requestHTTP(t, second.MixedPort, "Beta")
	after, _ := os.ReadFile(flatPath)
	if string(before) != string(after) {
		t.Fatal("subscription refresh changed flat snapshot")
	}
	other, err := m.AddSubscription("other", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SwitchSubscription(first.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	requestHTTP(t, second.MixedPort, "Beta")
	if err = m.Restart(second.ID); err != nil {
		t.Fatal(err)
	}
	requestHTTP(t, second.MixedPort, "Beta")
	if err = m.Stop(first.ID); err != nil {
		t.Fatal(err)
	}
	if m.Running(first) {
		t.Fatal("nohup process remained active")
	}
	requestHTTP(t, second.MixedPort, "Beta")
	if m.OwnedPID(flat) == 0 {
		t.Fatal("stopping one instance stopped flat instance")
	}
}
func TestRealCoreSystemdUserAndSystem(t *testing.T) {
	if os.Getenv("CLASHCTL_TEST_SYSTEMD") == "" {
		t.Skip("set CLASHCTL_TEST_SYSTEMD=1 to test user systemd")
	}
	m, config, _ := coreFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, config) }))
	defer server.Close()
	s, err := m.AddSubscription("main", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	backends := []string{"user"}
	if os.Getenv("CLASHCTL_TEST_SYSTEM") == "1" {
		backends = append(backends, "system")
	}
	for _, backend := range backends {
		t.Run(backend, func(t *testing.T) {
			i, err := m.CreateInstance(CreateOptions{Name: backend, Backend: backend}, s.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := m.DeleteInstance(i.ID); err != nil {
					t.Error(err)
				}
			})
			if err = m.Start(i.ID); err != nil {
				t.Fatal(err)
			}
			requestHTTP(t, i.MixedPort, "Alpha")
			if err = m.SetAutostart(i.ID, true); err != nil {
				t.Fatal(err)
			}
			if err = m.SetAutostart(i.ID, false); err != nil {
				t.Fatal(err)
			}
			if err = m.Stop(i.ID); err != nil {
				t.Fatal(err)
			}
			if m.Running(i) {
				t.Fatal("systemd instance still active")
			}
			if err = m.SetBackend(i.ID, "nohup"); err != nil {
				t.Fatal(err)
			}
			changed, err := m.Instance(i.ID)
			if err != nil {
				t.Fatal(err)
			}
			if changed.Backend != "nohup" {
				t.Fatal("failed to change backend")
			}
			if _, err = os.Stat(m.UnitPath(i)); !os.IsNotExist(err) {
				t.Fatal("old unit remains")
			}
			if err = m.Start(i.ID); err != nil {
				t.Fatal(err)
			}
			requestHTTP(t, i.MixedPort, "Alpha")
		})
	}
}

func TestRealCoreUnicodeAndSlashSelectors(t *testing.T) {
	m, config, _ := coreFixture(t)
	config = strings.ReplaceAll(config, "Alpha", "线路 / 东京")
	config = strings.ReplaceAll(config, "Beta", "线路 / 新加坡")
	config = strings.ReplaceAll(config, "PROXY", "代理 / 🌐")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, config) }))
	defer server.Close()
	s, err := m.AddSubscription("main", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	i, err := m.CreateInstance(CreateOptions{Name: "unicode"}, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.DeleteInstance(i.ID) })
	if err = m.Start(i.ID); err != nil {
		t.Fatal(err)
	}
	if err = m.SelectNode(i.ID, "代理 / 🌐", "线路 / 新加坡"); err != nil {
		t.Fatal(err)
	}
	requestHTTP(t, i.MixedPort, "Beta")
}
