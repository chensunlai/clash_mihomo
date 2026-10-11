package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Manager struct {
	Store  *Store
	client *http.Client
}

func New(root string) (*Manager, error) {
	store, err := NewStore(root)
	if err != nil {
		return nil, err
	}
	return &Manager{Store: store, client: &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil}}}, nil
}
func (m *Manager) Instances() ([]Instance, error) {
	state, err := m.Store.Read()
	if err != nil {
		return nil, err
	}
	items := []Instance{}
	for _, i := range state.Instances {
		items = append(items, i)
	}
	sort.Slice(items, func(a, b int) bool {
		return items[a].Created < items[b].Created || (items[a].Created == items[b].Created && items[a].ID < items[b].ID)
	})
	return items, nil
}
func (m *Manager) Subscriptions() ([]Subscription, error) {
	state, err := m.Store.Read()
	if err != nil {
		return nil, err
	}
	items := []Subscription{}
	for _, s := range state.Subscriptions {
		items = append(items, s)
	}
	sort.Slice(items, func(a, b int) bool { return items[a].Name < items[b].Name })
	return items, nil
}
func (m *Manager) Instance(identifier string) (Instance, error) {
	state, err := m.Store.Read()
	if err != nil {
		return Instance{}, err
	}
	if item, ok := state.Instances[identifier]; ok {
		return item, nil
	}
	matches := []Instance{}
	for id, item := range state.Instances {
		if item.Name == identifier || strings.HasPrefix(id, identifier) {
			matches = append(matches, item)
		}
	}
	if identifier == "" || len(matches) != 1 {
		return Instance{}, fmt.Errorf("找不到唯一实例：%s", identifier)
	}
	return matches[0], nil
}
func (m *Manager) Subscription(identifier string) (Subscription, error) {
	state, err := m.Store.Read()
	if err != nil {
		return Subscription{}, err
	}
	if item, ok := state.Subscriptions[identifier]; ok {
		return item, nil
	}
	matches := []Subscription{}
	for id, item := range state.Subscriptions {
		if item.Name == identifier || strings.HasPrefix(id, identifier) {
			matches = append(matches, item)
		}
	}
	if identifier == "" || len(matches) != 1 {
		return Subscription{}, fmt.Errorf("找不到唯一订阅：%s", identifier)
	}
	return matches[0], nil
}
func (m *Manager) AddSubscription(name, address string) (Subscription, error) {
	name = strings.TrimSpace(name)
	if err := validName(name); err != nil {
		return Subscription{}, err
	}
	parsed, err := url.Parse(address)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return Subscription{}, fmt.Errorf("请输入 HTTP/HTTPS Clash YAML 订阅链接")
	}
	data, err := Fetch(address, 45*time.Second)
	if err != nil {
		return Subscription{}, err
	}
	source, err := LoadYAML(data)
	if err != nil {
		return Subscription{}, err
	}
	encoded, err := EncodeYAML(source)
	if err != nil {
		return Subscription{}, err
	}
	item := Subscription{ID: ID(), Name: name, URL: address, Updated: Timestamp()}
	err = m.Store.Edit(func(state *State) error {
		for _, s := range state.Subscriptions {
			if s.Name == name {
				return fmt.Errorf("订阅名称已存在")
			}
		}
		if err := AtomicWrite(m.Store.SubscriptionPath(item.ID), encoded, 0600); err != nil {
			return err
		}
		state.Subscriptions[item.ID] = item
		return nil
	})
	return item, err
}
func (m *Manager) DeleteSubscription(identifier string) error {
	s, err := m.Subscription(identifier)
	if err != nil {
		return err
	}
	return m.Store.Lock("subscription-"+s.ID, func() error {
		if err := m.Store.Edit(func(state *State) error {
			for _, i := range state.Instances {
				if i.Subscription == s.ID {
					return fmt.Errorf("订阅仍被实例使用，请先切换或删除对应实例")
				}
			}
			delete(state.Subscriptions, s.ID)
			return nil
		}); err != nil {
			return err
		}
		err := os.Remove(m.Store.SubscriptionPath(s.ID))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	})
}
func (m *Manager) source(id string) (Config, error) {
	data, err := os.ReadFile(m.Store.SubscriptionPath(id))
	if err != nil {
		return nil, fmt.Errorf("订阅缓存不存在，请先更新订阅")
	}
	return LoadYAML(data)
}
func socketAvailable(port int) bool {
	address := fmt.Sprintf("0.0.0.0:%d", port)
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return false
	}
	defer listener.Close()
	packet, err := net.ListenPacket("udp4", address)
	if err != nil {
		return false
	}
	packet.Close()
	return true
}

type CreateOptions struct {
	Name    string
	Backend string
	Bind    string
	Port    int
}

func (m *Manager) reserve(options CreateOptions, kind, subscription string, entries []Entry) (Instance, error) {
	if options.Backend == "" {
		options.Backend = "nohup"
	}
	if options.Bind == "" {
		options.Bind = "127.0.0.1"
	}
	options.Name = strings.TrimSpace(options.Name)
	if err := validName(options.Name); err != nil {
		return Instance{}, err
	}
	if err := validBackend(options.Backend); err != nil {
		return Instance{}, err
	}
	if err := validateBind(options.Bind); err != nil {
		return Instance{}, err
	}
	i := Instance{ID: ID(), Name: options.Name, Kind: kind, Subscription: subscription, Backend: options.Backend, Bind: options.Bind, Secret: ID() + ID(), Mode: "rule", Created: Timestamp()}
	err := m.Store.Edit(func(state *State) error {
		occupied := map[int]bool{}
		for _, other := range state.Instances {
			if other.Name == i.Name {
				return fmt.Errorf("实例名称已存在")
			}
			for _, p := range other.Ports() {
				occupied[p] = true
			}
		}
		available := func(p int) bool { return !occupied[p] && socketAvailable(p) }
		findRange := func(first, count int) (int, error) {
			for p := first; p <= 65536-count; p++ {
				ok := true
				for n := p; n < p+count; n++ {
					if !available(n) {
						ok = false
						break
					}
				}
				if ok {
					return p, nil
				}
			}
			return 0, fmt.Errorf("找不到足够的连续空闲端口")
		}
		count := 1
		if kind == "flat" {
			count = len(entries)
		}
		if count < 1 {
			return fmt.Errorf("至少选择一个节点")
		}
		start := options.Port
		if start == 0 {
			var err error
			start, err = findRange(17890, count)
			if err != nil {
				return err
			}
		}
		if err := validatePort(start); err != nil {
			return err
		}
		if err := validatePort(start + count - 1); err != nil {
			return err
		}
		for p := start; p < start+count; p++ {
			if !available(p) {
				return fmt.Errorf("端口 %d 已被实例预留或其他程序占用", p)
			}
			occupied[p] = true
		}
		var err error
		i.APIPort, err = findRange(19090, 1)
		if err != nil {
			return err
		}
		if kind == "flat" {
			i.Entries = append([]Entry{}, entries...)
			for n := range i.Entries {
				i.Entries[n].Port = start + n
			}
		} else {
			i.MixedPort = start
		}
		state.Instances[i.ID] = i
		return nil
	})
	return i, err
}
func (m *Manager) validateConfig(i Instance, config Config) error {
	directory := m.Store.InstanceDir(i.ID)
	encoded, err := EncodeYAML(config)
	if err != nil {
		return err
	}
	candidate := filepath.Join(directory, "candidate.yaml")
	if err = AtomicWrite(candidate, encoded, 0600); err != nil {
		return err
	}
	defer os.Remove(candidate)
	if m.CoreInstalled() {
		return m.Validate(i, candidate)
	}
	return nil
}
func (m *Manager) complete(i Instance, source, config Config) (Instance, error) {
	err := m.Store.Lock("instance-"+i.ID, func() error {
		if err := m.validateConfig(i, config); err != nil {
			return err
		}
		directory := m.Store.InstanceDir(i.ID)
		data, err := EncodeYAML(source)
		if err != nil {
			return err
		}
		if err = AtomicWrite(filepath.Join(directory, "source.yaml"), data, 0600); err != nil {
			return err
		}
		data, err = EncodeYAML(config)
		if err != nil {
			return err
		}
		if err = AtomicWrite(filepath.Join(directory, "config.yaml"), data, 0600); err != nil {
			return err
		}
		i.Ready = true
		return m.Store.Edit(func(state *State) error { state.Instances[i.ID] = i; return nil })
	})
	if err != nil {
		_ = m.Store.Edit(func(state *State) error { delete(state.Instances, i.ID); return nil })
		_ = os.RemoveAll(m.Store.InstanceDir(i.ID))
	}
	return i, err
}
func (m *Manager) CreateInstance(options CreateOptions, subscription string) (Instance, error) {
	s, err := m.Subscription(subscription)
	if err != nil {
		return Instance{}, err
	}
	var i Instance
	err = m.Store.Lock("subscription-"+s.ID, func() error {
		if _, err := m.Subscription(s.ID); err != nil {
			return err
		}
		source, err := m.source(s.ID)
		if err != nil {
			return err
		}
		// Build once before reserving so malformed subscriptions cannot leak reservations.
		if _, err = OrdinaryConfig(source, Instance{Bind: options.Bind, Mode: "rule"}); err != nil {
			return err
		}
		i, err = m.reserve(options, "subscription", s.ID, nil)
		if err != nil {
			return err
		}
		config, err := OrdinaryConfig(source, i)
		if err != nil {
			return err
		}
		i, err = m.complete(i, source, config)
		return err
	})
	return i, err
}
func (m *Manager) CreateFlat(options CreateOptions, selections []Selection) (Instance, error) {
	nodes, entries, err := ExtractNodes(selections)
	if err != nil {
		return Instance{}, err
	}
	i, err := m.reserve(options, "flat", "", entries)
	if err != nil {
		return i, err
	}
	return m.complete(i, Config{"proxies": nodes}, FlatConfig(nodes, i))
}
func (m *Manager) Clone(identifier string, options CreateOptions) (Instance, error) {
	original, err := m.Instance(identifier)
	if err != nil {
		return Instance{}, err
	}
	if options.Backend == "" {
		options.Backend = original.Backend
	}
	if options.Bind == "" {
		options.Bind = original.Bind
	}
	if original.Kind == "subscription" {
		return m.CreateInstance(options, original.Subscription)
	}
	var result Instance
	err = m.Store.Lock("instance-"+original.ID, func() error {
		original, err = m.Instance(original.ID)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(m.Store.InstanceDir(original.ID), "source.yaml"))
		if err != nil {
			return err
		}
		source, err := LoadYAML(data)
		if err != nil {
			return err
		}
		nodes, err := parseNodes(source["proxies"])
		if err != nil {
			return err
		}
		result, err = m.reserve(options, "flat", "", original.Entries)
		if err != nil {
			return err
		}
		result, err = m.complete(result, source, FlatConfig(nodes, result))
		return err
	})
	return result, err
}
func (m *Manager) API(i Instance, method, path string, payload any, result any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", i.APIPort, path), body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+i.Secret)
	request.Header.Set("Content-Type", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return fmt.Errorf("无法访问控制 API，请确认实例已启动并查看日志")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("控制 API 返回 HTTP %d", response.StatusCode)
	}
	if result != nil {
		if err = json.NewDecoder(response.Body).Decode(result); err != nil {
			return fmt.Errorf("控制 API 响应无效")
		}
	}
	return nil
}

type Proxy struct {
	Type string   `json:"type"`
	Now  string   `json:"now"`
	All  []string `json:"all"`
}

func (m *Manager) proxies(i Instance) (map[string]Proxy, error) {
	var result struct {
		Proxies map[string]Proxy `json:"proxies"`
	}
	err := m.API(i, "GET", "/proxies", nil, &result)
	return result.Proxies, err
}
func (m *Manager) Groups(identifier string) (map[string]Proxy, error) {
	i, err := m.Instance(identifier)
	if err != nil {
		return nil, err
	}
	proxies, err := m.proxies(i)
	if err != nil {
		return nil, err
	}
	for name, p := range proxies {
		if p.Type != "Selector" || len(p.All) == 0 {
			delete(proxies, name)
		}
	}
	return proxies, nil
}
func (m *Manager) replaceConfig(i Instance, source Config) error {
	config, err := OrdinaryConfig(source, i)
	if err != nil {
		return err
	}
	if err = m.validateConfig(i, config); err != nil {
		return err
	}
	path := filepath.Join(m.Store.InstanceDir(i.ID), "config.yaml")
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	selections := map[string]string{}
	running := m.Running(i)
	if running {
		proxies, err := m.proxies(i)
		if err != nil {
			return err
		}
		for name, p := range proxies {
			if p.Type == "Selector" {
				selections[name] = p.Now
			}
		}
	}
	encoded, err := EncodeYAML(config)
	if err != nil {
		return err
	}
	if err = AtomicWrite(path, encoded, 0600); err != nil {
		return err
	}
	if running {
		if err = m.API(i, "PUT", "/configs?force=true", map[string]string{"path": path}, nil); err != nil {
			_ = AtomicWrite(path, old, 0600)
			_ = m.API(i, "PUT", "/configs?force=true", map[string]string{"path": path}, nil)
			return err
		}
		proxies, err := m.proxies(i)
		if err != nil {
			return err
		}
		for name, selected := range selections {
			for _, node := range proxies[name].All {
				if node == selected {
					if err = m.API(i, "PUT", "/proxies/"+url.PathEscape(name), map[string]string{"name": selected}, nil); err != nil {
						return err
					}
					break
				}
			}
		}
	}
	encoded, err = EncodeYAML(source)
	if err != nil {
		return err
	}
	return AtomicWrite(filepath.Join(m.Store.InstanceDir(i.ID), "source.yaml"), encoded, 0600)
}
func (m *Manager) RefreshSubscription(identifier string) error {
	s, err := m.Subscription(identifier)
	if err != nil {
		return err
	}
	return m.Store.Lock("subscription-"+s.ID, func() error {
		s, err = m.Subscription(s.ID)
		if err != nil {
			return err
		}
		data, err := Fetch(s.URL, 45*time.Second)
		if err != nil {
			return err
		}
		source, err := LoadYAML(data)
		if err != nil {
			return err
		}
		items, err := m.Instances()
		if err != nil {
			return err
		}
		for _, i := range items {
			if i.Subscription != s.ID {
				continue
			}
			err = m.Store.Lock("instance-"+i.ID, func() error {
				current, e := m.Instance(i.ID)
				if e != nil || current.Subscription != s.ID {
					return nil
				}
				config, e := OrdinaryConfig(source, current)
				if e != nil {
					return e
				}
				return m.validateConfig(current, config)
			})
			if err != nil {
				return err
			}
		}
		encoded, err := EncodeYAML(source)
		if err != nil {
			return err
		}
		if err = m.Store.Edit(func(state *State) error {
			current, found := state.Subscriptions[s.ID]
			if !found {
				return fmt.Errorf("订阅已被删除")
			}
			if err := AtomicWrite(m.Store.SubscriptionPath(s.ID), encoded, 0600); err != nil {
				return err
			}
			current.Updated = Timestamp()
			state.Subscriptions[s.ID] = current
			return nil
		}); err != nil {
			return err
		}
		items, err = m.Instances()
		if err != nil {
			return err
		}
		failures := []string{}
		for _, i := range items {
			if i.Subscription != s.ID {
				continue
			}
			err = m.Store.Lock("instance-"+i.ID, func() error {
				current, e := m.Instance(i.ID)
				if e != nil || current.Subscription != s.ID {
					return nil
				}
				return m.replaceConfig(current, source)
			})
			if err != nil {
				failures = append(failures, i.Name+": "+err.Error())
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("订阅缓存已更新，部分实例保留旧配置：%s", strings.Join(failures, "; "))
		}
		return nil
	})
}
func (m *Manager) SwitchSubscription(identifier, subscription string) error {
	s, err := m.Subscription(subscription)
	if err != nil {
		return err
	}
	i, err := m.Instance(identifier)
	if err != nil {
		return err
	}
	return m.Store.Lock("subscription-"+s.ID, func() error {
		if _, err := m.Subscription(s.ID); err != nil {
			return err
		}
		source, err := m.source(s.ID)
		if err != nil {
			return err
		}
		return m.Store.Lock("instance-"+i.ID, func() error {
			current, err := m.Instance(i.ID)
			if err != nil {
				return err
			}
			if current.Kind == "flat" {
				return fmt.Errorf("平铺是固定快照，不能切换订阅；请创建新的平铺实例")
			}
			if err = m.replaceConfig(current, source); err != nil {
				return err
			}
			return m.Store.Edit(func(state *State) error {
				current = state.Instances[i.ID]
				current.Subscription = s.ID
				state.Instances[i.ID] = current
				return nil
			})
		})
	})
}
func (m *Manager) Start(identifier string) error {
	i, err := m.Instance(identifier)
	if err != nil {
		return err
	}
	return m.Store.Lock("instance-"+i.ID, func() error {
		i, err = m.Instance(i.ID)
		if err != nil {
			return err
		}
		return m.startLocked(i)
	})
}
func (m *Manager) startLocked(i Instance) error {
	if !i.Ready {
		return fmt.Errorf("实例正在创建，请稍后再试")
	}
	if m.Running(i) {
		return nil
	}
	for _, p := range i.Ports() {
		if !socketAvailable(p) {
			return fmt.Errorf("端口 %d 被其他程序占用", p)
		}
	}
	if err := m.Validate(i, filepath.Join(m.Store.InstanceDir(i.ID), "config.yaml")); err != nil {
		return err
	}
	if err := m.startBackend(i); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if m.API(i, "GET", "/version", nil, nil) == nil && m.Running(i) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = m.stopBackend(i)
	return fmt.Errorf("实例启动失败，日志：%s", filepath.Join(m.Store.InstanceDir(i.ID), "mihomo.log"))
}
func (m *Manager) Stop(identifier string) error {
	i, err := m.Instance(identifier)
	if err != nil {
		return err
	}
	return m.Store.Lock("instance-"+i.ID, func() error {
		current, err := m.Instance(i.ID)
		if err != nil {
			return err
		}
		return m.stopBackend(current)
	})
}
func (m *Manager) Restart(identifier string) error {
	i, err := m.Instance(identifier)
	if err != nil {
		return err
	}
	return m.Store.Lock("instance-"+i.ID, func() error {
		current, err := m.Instance(i.ID)
		if err != nil {
			return err
		}
		if err = m.stopBackend(current); err != nil {
			return err
		}
		return m.startLocked(current)
	})
}
func (m *Manager) SetBackend(identifier, backend string) error {
	if err := validBackend(backend); err != nil {
		return err
	}
	i, err := m.Instance(identifier)
	if err != nil {
		return err
	}
	return m.Store.Lock("instance-"+i.ID, func() error {
		i, err = m.Instance(i.ID)
		if err != nil {
			return err
		}
		if i.Backend == backend {
			return nil
		}
		if m.Running(i) {
			return fmt.Errorf("请先停止实例，再切换后台方式")
		}
		if err = m.Unregister(i); err != nil {
			return err
		}
		return m.Store.Edit(func(state *State) error {
			i.Backend = backend
			i.Autostart = false
			state.Instances[i.ID] = i
			return nil
		})
	})
}
func (m *Manager) SetAutostart(identifier string, enabled bool) error {
	i, err := m.Instance(identifier)
	if err != nil {
		return err
	}
	return m.Store.Lock("instance-"+i.ID, func() error {
		i, err = m.Instance(i.ID)
		if err != nil {
			return err
		}
		if i.Backend == "nohup" {
			return fmt.Errorf("nohup 不支持自启，请选择 systemd 用户或系统服务")
		}
		if err = m.Register(i); err != nil {
			return err
		}
		action := "disable"
		if enabled {
			action = "enable"
		}
		if _, err = m.service(i, action, m.UnitName(i)); err != nil {
			return err
		}
		return m.Store.Edit(func(state *State) error { i.Autostart = enabled; state.Instances[i.ID] = i; return nil })
	})
}
func (m *Manager) DeleteInstance(identifier string) error {
	i, err := m.Instance(identifier)
	if err != nil {
		return err
	}
	return m.Store.Lock("instance-"+i.ID, func() error {
		i, err = m.Instance(i.ID)
		if err != nil {
			return err
		}
		if err = m.stopBackend(i); err != nil {
			return err
		}
		if err = m.Unregister(i); err != nil {
			return err
		}
		if err = m.Store.Edit(func(state *State) error { delete(state.Instances, i.ID); return nil }); err != nil {
			return err
		}
		return os.RemoveAll(m.Store.InstanceDir(i.ID))
	})
}
func (m *Manager) SelectNode(identifier, group, node string) error {
	i, err := m.Instance(identifier)
	if err != nil {
		return err
	}
	return m.Store.Lock("instance-"+i.ID, func() error {
		i, err = m.Instance(i.ID)
		if err != nil {
			return err
		}
		if i.Kind == "flat" {
			return fmt.Errorf("平铺端口固定绑定节点，不能切换")
		}
		groups, err := m.Groups(i.ID)
		if err != nil {
			return err
		}
		for _, name := range groups[group].All {
			if name == node {
				return m.API(i, "PUT", "/proxies/"+url.PathEscape(group), map[string]string{"name": node}, nil)
			}
		}
		return fmt.Errorf("节点不属于此可切换策略组")
	})
}
func (m *Manager) Delay(identifier, node string) (int, error) {
	i, err := m.Instance(identifier)
	if err != nil {
		return 0, err
	}
	var result struct {
		Delay int `json:"delay"`
	}
	query := url.Values{"url": {"https://www.gstatic.com/generate_204"}, "timeout": {"5000"}}
	err = m.API(i, "GET", "/proxies/"+url.PathEscape(node)+"/delay?"+query.Encode(), nil, &result)
	return result.Delay, err
}
func (m *Manager) SetMode(identifier, mode string) error {
	if mode != "rule" && mode != "global" && mode != "direct" {
		return fmt.Errorf("模式需为 rule / global / direct")
	}
	i, err := m.Instance(identifier)
	if err != nil {
		return err
	}
	return m.Store.Lock("instance-"+i.ID, func() error {
		i, err = m.Instance(i.ID)
		if err != nil {
			return err
		}
		if i.Kind == "flat" {
			return fmt.Errorf("平铺实例始终使用固定节点")
		}
		path := filepath.Join(m.Store.InstanceDir(i.ID), "config.yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		config, err := LoadYAML(data)
		if err != nil {
			return err
		}
		config["mode"] = mode
		if m.Running(i) {
			if err = m.API(i, "PATCH", "/configs", map[string]string{"mode": mode}, nil); err != nil {
				return err
			}
		}
		data, err = EncodeYAML(config)
		if err != nil {
			return err
		}
		if err = AtomicWrite(path, data, 0600); err != nil {
			return err
		}
		return m.Store.Edit(func(state *State) error { i.Mode = mode; state.Instances[i.ID] = i; return nil })
	})
}
func (m *Manager) Logs(identifier string) (string, error) {
	i, err := m.Instance(identifier)
	if err != nil {
		return "", err
	}
	file, err := os.Open(filepath.Join(m.Store.InstanceDir(i.ID), "mihomo.log"))
	if os.IsNotExist(err) {
		return "暂无日志。", nil
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > 65536 {
		_, _ = file.Seek(info.Size()-65536, io.SeekStart)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 120 {
		lines = lines[len(lines)-120:]
	}
	return strings.Join(lines, "\n"), nil
}
func (m *Manager) ExportFlat(identifier, destination string) error {
	i, err := m.Instance(identifier)
	if err != nil {
		return err
	}
	if i.Kind != "flat" {
		return fmt.Errorf("仅平铺实例支持导出固定快照")
	}
	return m.Store.Lock("instance-"+i.ID, func() error {
		path, err := ExpandPath(destination)
		if err != nil {
			return err
		}
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			return fmt.Errorf("导出目标已存在或不可访问，请使用新文件名")
		}
		data, err := os.ReadFile(filepath.Join(m.Store.InstanceDir(i.ID), "config.yaml"))
		if err != nil {
			return err
		}
		config, err := LoadYAML(data)
		if err != nil {
			return err
		}
		delete(config, "secret")
		delete(config, "external-controller")
		data, err = EncodeYAML(config)
		if err != nil {
			return err
		}
		return AtomicCreate(path, data)
	})
}

type InstanceStatus struct {
	Instance
	Running bool `json:"running"`
}
type Status struct {
	Subscriptions []Subscription   `json:"subscriptions"`
	Instances     []InstanceStatus `json:"instances"`
	CoreInstalled bool             `json:"core_installed"`
}

func (m *Manager) Status() (Status, error) {
	subs, err := m.Subscriptions()
	if err != nil {
		return Status{}, err
	}
	items, err := m.Instances()
	if err != nil {
		return Status{}, err
	}
	status := Status{Subscriptions: subs, Instances: []InstanceStatus{}, CoreInstalled: m.CoreInstalled()}
	for index := range status.Subscriptions {
		status.Subscriptions[index].URL = ""
	}
	for _, i := range items {
		running := m.Running(i)
		i.Secret = ""
		status.Instances = append(status.Instances, InstanceStatus{Instance: i, Running: running})
	}
	return status, nil
}
