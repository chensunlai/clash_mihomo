package manager

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Config = map[string]any
type Node = map[string]any
type Selection struct {
	Path  string
	Names []string
}

func LoadYAML(data []byte) (Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("不是有效的 Clash YAML 配置")
	}
	if c == nil {
		return nil, fmt.Errorf("需要 Clash YAML 配置，不支持 base64 / URI 格式订阅")
	}
	if c["proxies"] == nil && c["proxy-providers"] == nil {
		return nil, fmt.Errorf("配置没有 proxies 或 proxy-providers")
	}
	return c, nil
}
func EncodeYAML(c Config) ([]byte, error) { return yaml.Marshal(c) }
func cloneConfig(c Config) Config {
	data, _ := yaml.Marshal(c)
	var result Config
	_ = yaml.Unmarshal(data, &result)
	return result
}
func stringValue(v any) string    { s, _ := v.(string); return s }
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func validatePort(p int) error {
	if p < 1024 || p > 65535 {
		return fmt.Errorf("端口需为 1024–65535")
	}
	return nil
}
func validateBind(b string) error {
	if ip := net.ParseIP(b); ip == nil || ip.To4() == nil {
		return fmt.Errorf("请输入 IPv4 监听地址，例如 127.0.0.1 或 0.0.0.0")
	}
	return nil
}
func baseConfig(i Instance) Config {
	return Config{"allow-lan": i.Bind != "127.0.0.1", "bind-address": i.Bind, "mode": i.Mode, "log-level": "info",
		"external-controller": fmt.Sprintf("127.0.0.1:%d", i.APIPort), "secret": i.Secret, "profile": map[string]any{"store-selected": true}}
}
func OrdinaryConfig(source Config, i Instance) (Config, error) {
	c := cloneConfig(source)
	for _, key := range []string{"port", "socks-port", "mixed-port", "redir-port", "tproxy-port", "listeners", "tun", "tunnels",
		"external-controller", "external-controller-tls", "external-controller-unix", "external-controller-pipe", "external-ui", "external-ui-name", "external-ui-url",
		"secret", "authentication", "skip-auth-prefixes", "lan-allowed-ips", "lan-disallowed-ips", "routing-mark", "interface-name", "iptables", "external-doh-server"} {
		delete(c, key)
	}
	for key, value := range baseConfig(i) {
		c[key] = value
	}
	c["mixed-port"] = i.MixedPort
	if dns := object(c["dns"]); dns != nil {
		delete(dns, "listen")
	}
	for _, section := range []string{"proxy-providers", "rule-providers"} {
		if c[section] == nil {
			continue
		}
		providers := object(c[section])
		if providers == nil {
			return nil, fmt.Errorf("%s 必须是对象", section)
		}
		keys := []string{}
		for key := range providers {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for index, key := range keys {
			p := object(providers[key])
			if p == nil {
				return nil, fmt.Errorf("provider %s 格式无效", key)
			}
			if p["type"] == "file" {
				return nil, fmt.Errorf("订阅包含 file provider；请使用 HTTP/inline provider 或平铺导入本地快照")
			}
			if p["path"] != nil || p["type"] == "http" {
				p["path"] = fmt.Sprintf("providers/%s-%d.yaml", section, index)
			}
		}
	}
	if groups, ok := c["proxy-groups"].([]any); !ok || len(groups) == 0 {
		nodes, err := parseNodes(c["proxies"])
		if err != nil {
			return nil, err
		}
		names := []string{}
		for _, n := range nodes {
			names = append(names, stringValue(n["name"]))
		}
		group := map[string]any{"name": "PROXY", "type": "select", "proxies": names}
		if providers := object(c["proxy-providers"]); len(providers) > 0 {
			keys := []string{}
			for name := range providers {
				keys = append(keys, name)
			}
			sort.Strings(keys)
			group["use"] = keys
		} else if len(names) == 0 {
			return nil, fmt.Errorf("配置没有可用节点")
		}
		c["proxy-groups"] = []any{group}
		if c["rules"] == nil {
			c["rules"] = []string{"MATCH,PROXY"}
		}
	}
	return c, nil
}
func parseNodes(value any) ([]Node, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("proxies/payload 必须是节点列表")
	}
	nodes := []Node{}
	for _, item := range items {
		n := object(item)
		if n == nil || stringValue(n["name"]) == "" || stringValue(n["type"]) == "" {
			return nil, fmt.Errorf("存在无效节点：必须包含 name 和 type")
		}
		nodes = append(nodes, Node(n))
	}
	return nodes, nil
}
func LocalNodes(path string) ([]Node, error) {
	path, err := ExpandPath(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取 %s", path)
	}
	c, err := LoadYAML(data)
	if err != nil {
		return nil, err
	}
	nodes, err := parseNodes(c["proxies"])
	if err != nil {
		return nil, err
	}
	providers := object(c["proxy-providers"])
	keys := []string{}
	for key := range providers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p := object(providers[key])
		if p == nil {
			return nil, fmt.Errorf("provider %s 无效", key)
		}
		payload := p["payload"]
		if payload == nil {
			cache := stringValue(p["path"])
			if cache == "" {
				continue
			}
			if !filepath.IsAbs(cache) {
				cache = filepath.Join(filepath.Dir(path), cache)
			}
			data, err := os.ReadFile(cache)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("无法读取 provider %s 本地缓存", key)
			}
			cached, err := LoadYAML(data)
			if err != nil {
				return nil, err
			}
			payload = cached["proxies"]
		}
		more, err := parseNodes(payload)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, more...)
	}
	names := map[string]bool{}
	for _, n := range nodes {
		name := stringValue(n["name"])
		if names[name] {
			return nil, fmt.Errorf("%s 含重名节点 %s，请先在源文件中区分名称", filepath.Base(path), name)
		}
		names[name] = true
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("文件没有本地节点；HTTP provider 需要先保存本地缓存（平铺不会联网拉取）")
	}
	return nodes, nil
}
func ExtractNodes(selections []Selection) ([]Node, []Entry, error) {
	extracted := []Node{}
	entries := []Entry{}
	for sourceIndex, selection := range selections {
		path, err := ExpandPath(selection.Path)
		if err != nil {
			return nil, nil, err
		}
		nodes, err := LocalNodes(path)
		if err != nil {
			return nil, nil, err
		}
		byName := map[string]Node{}
		for _, n := range nodes {
			byName[stringValue(n["name"])] = n
		}
		if len(selection.Names) == 0 {
			return nil, nil, fmt.Errorf("%s 未选择节点", filepath.Base(path))
		}
		included, visiting := map[string]bool{}, map[string]bool{}
		ordered := []string{}
		var include func(string) error
		include = func(name string) error {
			if visiting[name] {
				return fmt.Errorf("节点 %s 的 dialer-proxy 存在循环", name)
			}
			if included[name] || name == "DIRECT" || name == "REJECT" {
				return nil
			}
			n, found := byName[name]
			if !found {
				return fmt.Errorf("%s 找不到节点 %s，原策略组不能用作平铺节点或依赖", filepath.Base(path), name)
			}
			visiting[name] = true
			if dep := stringValue(n["dialer-proxy"]); dep != "" {
				if err := include(dep); err != nil {
					return err
				}
			}
			delete(visiting, name)
			included[name] = true
			ordered = append(ordered, name)
			return nil
		}
		for _, name := range selection.Names {
			if err := include(name); err != nil {
				return nil, nil, err
			}
		}
		renames := map[string]string{}
		for _, name := range ordered {
			renames[name] = fmt.Sprintf("s%d · %s", sourceIndex+1, name)
		}
		for _, name := range ordered {
			n := byName[name]
			n["name"] = renames[name]
			if renamed := renames[stringValue(n["dialer-proxy"])]; renamed != "" {
				n["dialer-proxy"] = renamed
			}
			for _, key := range []string{"certificate", "private-key"} {
				value := stringValue(n[key])
				if value == "" || strings.Contains(value, "\n") {
					continue
				}
				file := value
				if !filepath.IsAbs(file) {
					file = filepath.Join(filepath.Dir(path), file)
				}
				if info, err := os.Stat(file); err == nil && !info.IsDir() {
					content, err := os.ReadFile(file)
					if err != nil {
						return nil, nil, err
					}
					n[key] = string(content)
				}
			}
			extracted = append(extracted, n)
		}
		seen := map[string]bool{}
		for _, name := range selection.Names {
			if seen[name] {
				continue
			}
			seen[name] = true
			if renames[name] == "" {
				return nil, nil, fmt.Errorf("请选择具体代理节点")
			}
			entries = append(entries, Entry{Node: renames[name], Original: name, Source: path})
		}
	}
	if len(entries) == 0 {
		return nil, nil, fmt.Errorf("至少选择一个节点")
	}
	return extracted, entries, nil
}
func FlatConfig(nodes []Node, i Instance) Config {
	c := baseConfig(i)
	c["mode"] = "rule"
	c["ipv6"] = false
	c["proxies"] = nodes
	c["rules"] = []string{"MATCH,REJECT"}
	listeners := []any{}
	for index, e := range i.Entries {
		listeners = append(listeners, map[string]any{"name": fmt.Sprintf("flat-%d", index+1), "type": "mixed", "listen": i.Bind, "port": e.Port, "udp": true, "proxy": e.Node})
	}
	c["listeners"] = listeners
	return c
}
