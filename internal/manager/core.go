package manager

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const CoreVersion = "v1.19.32"

var Backends = []string{"nohup", "user", "system"}

func validBackend(backend string) error {
	for _, b := range Backends {
		if b == backend {
			return nil
		}
	}
	return fmt.Errorf("后台方式需为 nohup / user / system")
}
func Fetch(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("下载地址无效")
	}
	request.Header.Set("User-Agent", "clash.meta/clashctl")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("下载失败，请检查网络或代理设置")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("下载失败：HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 128*1024*1024+1))
	if err != nil {
		return nil, fmt.Errorf("下载中断")
	}
	if len(data) > 128*1024*1024 {
		return nil, fmt.Errorf("下载文件过大")
	}
	return data, nil
}
func (m *Manager) Binary() string {
	path := filepath.Join(m.Store.Root, "bin", "mihomo")
	if binary := os.Getenv("CLASHCTL_CORE"); binary != "" {
		absolute, _ := ExpandPath(binary)
		path = absolute
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}
func (m *Manager) CoreInstalled() bool {
	info, err := os.Stat(m.Binary())
	return err == nil && !info.IsDir() && info.Mode()&0111 != 0
}
func runCommand(timeout time.Duration, command string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result, err := exec.CommandContext(ctx, command, args...).CombinedOutput()
	if ctx.Err() != nil {
		return result, fmt.Errorf("命令超时")
	}
	return result, err
}
func (m *Manager) CoreInfo() (string, error) {
	output, err := runCommand(10*time.Second, m.Binary(), "-v")
	if err != nil {
		return "", fmt.Errorf("核心无法执行，请检查安装或 CLASHCTL_CORE")
	}
	return strings.TrimSpace(string(output)), nil
}
func (m *Manager) InstallCore() (string, error) {
	var version string
	err := m.Store.Lock("core", func() error {
		if m.CoreInstalled() {
			var err error
			version, err = m.CoreInfo()
			return err
		}
		if os.Getenv("CLASHCTL_CORE") != "" {
			return fmt.Errorf("CLASHCTL_CORE 指定的核心不可执行")
		}
		arch := runtime.GOARCH
		if arch == "amd64" {
			arch = "amd64-compatible"
		}
		if runtime.GOOS != "linux" || (arch != "amd64-compatible" && arch != "arm64") {
			return fmt.Errorf("自动安装核心支持 Linux amd64/arm64")
		}
		asset := fmt.Sprintf("mihomo-linux-%s-%s.gz", arch, CoreVersion)
		data, err := Fetch("https://github.com/MetaCubeX/mihomo/releases/download/"+CoreVersion+"/"+asset, 180*time.Second)
		if err != nil {
			return err
		}
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("核心下载不是有效 gzip")
		}
		defer reader.Close()
		content, err := io.ReadAll(io.LimitReader(reader, 160*1024*1024+1))
		if err != nil || len(content) > 160*1024*1024 {
			return fmt.Errorf("核心解压失败")
		}
		candidate := m.Binary() + ".download"
		if err = AtomicWrite(candidate, content, 0700); err != nil {
			return err
		}
		defer os.Remove(candidate)
		output, err := runCommand(10*time.Second, candidate, "-v")
		if err != nil || !strings.Contains(string(output), strings.TrimPrefix(CoreVersion, "v")) {
			return fmt.Errorf("核心版本验证失败")
		}
		if err = os.Rename(candidate, m.Binary()); err != nil {
			return err
		}
		version = strings.TrimSpace(string(output))
		return nil
	})
	return version, err
}
func (m *Manager) Validate(i Instance, config string) error {
	if !m.CoreInstalled() {
		return fmt.Errorf("尚未安装 mihomo，请运行 clashctl install-core 或在 TUI 中安装核心")
	}
	output, err := runCommand(120*time.Second, m.Binary(), "-t", "-d", m.Store.InstanceDir(i.ID), "-f", config)
	if err != nil {
		log := filepath.Join(m.Store.InstanceDir(i.ID), "validation.log")
		_ = AtomicWrite(log, output, 0600)
		return fmt.Errorf("配置验证失败，详情见 %s", log)
	}
	return nil
}
func (m *Manager) UnitName(i Instance) string {
	namespace := sha256.Sum256([]byte(m.Store.Root))
	return "clashctl-" + hex.EncodeToString(namespace[:4]) + "-" + i.ID + ".service"
}
func (m *Manager) UnitPath(i Instance) string {
	if i.Backend == "user" {
		config := os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			home, _ := os.UserHomeDir()
			config = filepath.Join(home, ".config")
		}
		return filepath.Join(config, "systemd", "user", m.UnitName(i))
	}
	return filepath.Join("/etc/systemd/system", m.UnitName(i))
}
func (m *Manager) service(i Instance, args ...string) ([]byte, error) {
	command := "systemctl"
	if i.Backend == "user" {
		args = append([]string{"--user"}, args...)
	} else if os.Geteuid() != 0 {
		command = "sudo"
		args = append([]string{"-n", "systemctl"}, args...)
	}
	output, err := runCommand(45*time.Second, command, args...)
	if err != nil {
		if strings.Contains(string(output), "password") || strings.Contains(string(output), "密码") {
			return output, fmt.Errorf("系统服务需要 sudo 权限，请先在终端运行 sudo -v")
		}
		return output, fmt.Errorf("systemd 操作失败：%s", strings.TrimSpace(string(output)))
	}
	return output, nil
}
func unitQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "%", "%%")
	return "\"" + s + "\""
}
func execQuote(s string) string { return unitQuote(strings.ReplaceAll(s, "$", "$$")) }
func (m *Manager) Register(i Instance) error {
	directory := m.Store.InstanceDir(i.ID)
	// The system manager opens append logs as root even when User= is set.
	// Create the file as its owner first, so all three backends can share it.
	log, err := os.OpenFile(filepath.Join(directory, "mihomo.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("无法创建当前用户的实例日志：%w", err)
	}
	if err = log.Close(); err != nil {
		return err
	}
	account, err := user.Current()
	if err != nil {
		return err
	}
	identity := ""
	target := "default.target"
	if i.Backend == "system" {
		identity = "User=" + account.Username + "\nGroup=" + account.Gid + "\n"
		target = "multi-user.target"
	}
	// Path directives use the entire value, including spaces; only ExecStart uses shell-style quotes.
	unit := "[Unit]\nDescription=clashctl managed proxy instance\nAfter=network.target\n\n[Service]\nType=exec\n" + identity +
		"WorkingDirectory=" + strings.ReplaceAll(directory, "%", "%%") + "\nExecStart=" + execQuote(m.Binary()) + " -d " + execQuote(directory) + " -f " + execQuote(filepath.Join(directory, "config.yaml")) + "\n" +
		"Restart=always\nRestartSec=3\nTimeoutStopSec=15\nUMask=0077\nStandardOutput=append:" + strings.ReplaceAll(filepath.Join(directory, "mihomo.log"), "%", "%%") + "\nStandardError=append:" + strings.ReplaceAll(filepath.Join(directory, "mihomo.log"), "%", "%%") + "\n\n[Install]\nWantedBy=" + target + "\n"
	path := m.UnitPath(i)
	if existing, readErr := os.ReadFile(path); readErr == nil && string(existing) == unit {
		return nil
	}
	if i.Backend == "user" || os.Geteuid() == 0 {
		err = AtomicWrite(path, []byte(unit), 0644)
	} else {
		staging := filepath.Join(directory, "service.unit")
		if err = AtomicWrite(staging, []byte(unit), 0644); err != nil {
			return err
		}
		defer os.Remove(staging)
		_, err = runCommand(30*time.Second, "sudo", "-n", "install", "-m", "0644", staging, path)
		if err != nil {
			return fmt.Errorf("安装系统服务需要 sudo 权限，请先运行 sudo -v")
		}
	}
	if err != nil {
		return err
	}
	_, err = m.service(i, "daemon-reload")
	return err
}
func (m *Manager) Unregister(i Instance) error {
	if i.Backend == "nohup" {
		return nil
	}
	if _, err := os.Stat(m.UnitPath(i)); os.IsNotExist(err) {
		return nil
	}
	if _, err := m.service(i, "disable", "--now", m.UnitName(i)); err != nil {
		return err
	}
	if i.Backend == "user" || os.Geteuid() == 0 {
		if err := os.Remove(m.UnitPath(i)); err != nil {
			return err
		}
	} else {
		if _, err := runCommand(30*time.Second, "sudo", "-n", "unlink", m.UnitPath(i)); err != nil {
			return fmt.Errorf("无法移除此实例的系统服务")
		}
	}
	_, err := m.service(i, "daemon-reload")
	return err
}

type ProcessRecord struct {
	PID        int    `json:"pid"`
	StartTicks string `json:"start_ticks"`
}

func processIdentity(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	text := string(data)
	offset := strings.LastIndex(text, ")")
	if offset < 0 {
		return ""
	}
	fields := strings.Fields(text[offset+1:])
	if len(fields) < 20 || fields[0] == "Z" {
		return ""
	}
	return fields[19]
}
func (m *Manager) OwnedPID(i Instance) int {
	data, err := os.ReadFile(filepath.Join(m.Store.InstanceDir(i.ID), "process.json"))
	if err != nil {
		return 0
	}
	var record ProcessRecord
	if json.Unmarshal(data, &record) != nil || record.PID <= 0 || record.StartTicks == "" || processIdentity(record.PID) != record.StartTicks {
		return 0
	}
	command, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", record.PID))
	if err != nil {
		return 0
	}
	found := false
	for _, arg := range bytes.Split(command, []byte{0}) {
		if string(arg) == m.Store.InstanceDir(i.ID) {
			found = true
		}
	}
	if !found {
		return 0
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", record.PID))
	if err != nil || exe != m.Binary() {
		return 0
	}
	return record.PID
}
func (m *Manager) Running(i Instance) bool {
	if i.Backend == "nohup" {
		return m.OwnedPID(i) != 0
	}
	args := []string{}
	if i.Backend == "user" {
		args = append(args, "--user")
	}
	args = append(args, "is-active", "--quiet", m.UnitName(i))
	_, err := runCommand(5*time.Second, "systemctl", args...)
	return err == nil
}
func (m *Manager) startBackend(i Instance) error {
	if i.Backend != "nohup" {
		if err := m.Register(i); err != nil {
			return err
		}
		if _, err := m.service(i, "start", m.UnitName(i)); err != nil {
			return err
		}
		if i.Autostart {
			_, err := m.service(i, "enable", m.UnitName(i))
			return err
		}
		return nil
	}
	directory := m.Store.InstanceDir(i.ID)
	log, err := os.OpenFile(filepath.Join(directory, "mihomo.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	command := exec.Command("nohup", m.Binary(), "-d", directory, "-f", filepath.Join(directory, "config.yaml"))
	command.Dir = directory
	command.Stdout = log
	command.Stderr = log
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = command.Start(); err != nil {
		return fmt.Errorf("nohup 启动失败：%w", err)
	}
	record := ProcessRecord{PID: command.Process.Pid, StartTicks: processIdentity(command.Process.Pid)}
	data, _ := json.Marshal(record)
	if err = AtomicWrite(filepath.Join(directory, "process.json"), data, 0600); err != nil {
		_ = command.Process.Signal(syscall.SIGTERM)
	}
	go func() { _ = command.Wait() }()
	return err
}
func (m *Manager) stopBackend(i Instance) error {
	if i.Backend != "nohup" {
		if _, err := os.Stat(m.UnitPath(i)); os.IsNotExist(err) {
			return nil
		}
		_, err := m.service(i, "stop", m.UnitName(i))
		return err
	}
	pid := m.OwnedPID(i)
	if pid != 0 {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			return err
		}
		deadline := time.Now().Add(10 * time.Second)
		for m.OwnedPID(i) != 0 && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if m.OwnedPID(i) != 0 {
			return fmt.Errorf("实例尚未退出，请查看日志")
		}
	}
	err := os.Remove(filepath.Join(m.Store.InstanceDir(i.ID), "process.json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func (m *Manager) RuntimeDescription(i Instance) string {
	if i.Backend == "nohup" {
		if pid := m.OwnedPID(i); pid != 0 {
			return "PID " + strconv.Itoa(pid)
		}
		return "已停止"
	}
	return m.UnitName(i)
}
