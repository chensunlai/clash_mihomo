package manager

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Subscription struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	Updated string `json:"updated"`
}
type Entry struct {
	Node     string `json:"node"`
	Original string `json:"original"`
	Source   string `json:"source"`
	Port     int    `json:"port"`
}
type Instance struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	Subscription string  `json:"subscription,omitempty"`
	Backend      string  `json:"backend"`
	Bind         string  `json:"bind"`
	MixedPort    int     `json:"mixed_port,omitempty"`
	APIPort      int     `json:"api_port"`
	Secret       string  `json:"secret,omitempty"`
	Mode         string  `json:"mode"`
	Autostart    bool    `json:"autostart"`
	Ready        bool    `json:"ready"`
	Created      string  `json:"created"`
	Entries      []Entry `json:"entries,omitempty"`
}

func (i Instance) Ports() []int {
	ports := []int{i.APIPort}
	if i.Kind == "flat" {
		for _, e := range i.Entries {
			ports = append(ports, e.Port)
		}
	} else {
		ports = append(ports, i.MixedPort)
	}
	return ports
}

type State struct {
	Version       int                     `json:"version"`
	Subscriptions map[string]Subscription `json:"subscriptions"`
	Instances     map[string]Instance     `json:"instances"`
}
type Store struct{ Root string }

func NewStore(root string) (*Store, error) {
	if root == "" {
		root = os.Getenv("CLASHCTL_HOME")
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, ".local", "clash_mihomo")
		if exe, err := os.Executable(); err == nil {
			if real, err := filepath.EvalSymlinks(exe); err == nil {
				exe = real
			}
			if filepath.Base(filepath.Dir(exe)) == "bin" && filepath.Base(exe) == "clashctl" {
				root = filepath.Dir(filepath.Dir(exe))
			}
		}
	}
	root, err := ExpandPath(root)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(root, "\r\n\x00") {
		return nil, fmt.Errorf("安装目录不能包含换行或 NUL 字符")
	}
	for _, d := range []string{root, filepath.Join(root, "locks"), filepath.Join(root, "subscriptions"), filepath.Join(root, "instances"), filepath.Join(root, "bin")} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return nil, err
		}
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	return &Store{Root: root}, nil
}
func ExpandPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return filepath.Abs(path)
}
func AtomicWrite(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(content); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), mode); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// AtomicCreate publishes an export without replacing a concurrently created file.
func AtomicCreate(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".export-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(content); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Link(f.Name(), path)
}
func (s *Store) Lock(key string, fn func() error) error {
	f, err := os.OpenFile(filepath.Join(s.Root, "locks", key+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
func (s *Store) read() (State, error) {
	state := State{Version: 1, Subscriptions: map[string]Subscription{}, Instances: map[string]Instance{}}
	data, err := os.ReadFile(filepath.Join(s.Root, "state.json"))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(data, &state); err != nil || state.Version != 1 || state.Subscriptions == nil || state.Instances == nil {
		return state, fmt.Errorf("管理状态无效；未覆盖 state.json")
	}
	return state, nil
}
func (s *Store) Read() (State, error) {
	var state State
	err := s.Lock("state", func() error { var err error; state, err = s.read(); return err })
	return state, err
}
func (s *Store) Edit(fn func(*State) error) error {
	return s.Lock("state", func() error {
		state, err := s.read()
		if err != nil {
			return err
		}
		if err = fn(&state); err != nil {
			return err
		}
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		return AtomicWrite(filepath.Join(s.Root, "state.json"), data, 0600)
	})
}
func (s *Store) InstanceDir(id string) string { return filepath.Join(s.Root, "instances", id) }
func (s *Store) SubscriptionPath(id string) string {
	return filepath.Join(s.Root, "subscriptions", id+".yaml")
}
func ID() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}
func Timestamp() string { return time.Now().UTC().Format(time.RFC3339) }
func validName(name string) error {
	if strings.TrimSpace(name) == "" || len([]rune(name)) > 80 {
		return fmt.Errorf("名称需为 1–80 个字符")
	}
	for _, c := range name {
		if c < 32 || c == 127 {
			return fmt.Errorf("名称不能包含控制字符")
		}
	}
	return nil
}
