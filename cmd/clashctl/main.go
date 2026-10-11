package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/chensunlai/clash_mihomo/internal/manager"
	"github.com/chensunlai/clash_mihomo/internal/tui"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}
func printJSON(value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err == nil {
		fmt.Println(string(encoded))
	}
	return err
}
func printInstance(i manager.Instance, err error) error {
	if err != nil {
		return err
	}
	i.Secret = ""
	return printJSON(i)
}
func flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	return f
}
func createFlags(f *flag.FlagSet, defaultBackend string) *manager.CreateOptions {
	o := &manager.CreateOptions{}
	f.StringVar(&o.Name, "name", "", "实例名称")
	f.StringVar(&o.Backend, "backend", defaultBackend, "nohup / user / system")
	f.StringVar(&o.Bind, "bind", "", "监听 IPv4（默认 127.0.0.1）")
	f.IntVar(&o.Port, "port", 0, "mixed 起始端口（0 为自动分配）")
	return o
}
func oneArg(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("需要且仅需要一个实例名称或 ID")
	}
	return args[0], nil
}
func run(args []string) error {
	global := flags("clashctl")
	home := global.String("home", "", "安装/管理目录（默认 ~/.local/clash_mihomo）")
	showVersion := global.Bool("version", false, "查看版本")
	global.Usage = usage
	if err := global.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	args = global.Args()
	if *showVersion {
		fmt.Println("clashctl", version)
		return nil
	}
	if len(args) > 0 && (args[0] == "version" || args[0] == "--version") {
		fmt.Println("clashctl", version)
		return nil
	}
	if len(args) > 0 && (args[0] == "help") {
		usage()
		return nil
	}
	m, err := manager.New(*home)
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "tui" {
		return tui.Run(m)
	}
	command := args[0]
	args = args[1:]
	switch command {
	case "install-core":
		info, err := m.InstallCore()
		if err == nil {
			fmt.Println(info)
		}
		return err
	case "status":
		status, err := m.Status()
		if err != nil {
			return err
		}
		return printJSON(status)
	case "sub":
		return subscriptions(m, args)
	case "new":
		f := flags(command)
		o := createFlags(f, "nohup")
		sub := f.String("sub", "", "订阅名称或 ID")
		if err = f.Parse(args); err != nil {
			return err
		}
		if *sub == "" {
			return fmt.Errorf("需要 --sub")
		}
		return printInstance(m.CreateInstance(*o, *sub))
	case "flat":
		f := flags(command)
		o := createFlags(f, "nohup")
		selections := []manager.Selection{}
		current := -1
		f.Func("file", "本地 YAML 文件；可重复指定", func(path string) error {
			selections = append(selections, manager.Selection{Path: path})
			current = len(selections) - 1
			return nil
		})
		f.Func("node", "从最近的 --file 中提取精确名称节点；可重复", func(name string) error {
			if current < 0 {
				return fmt.Errorf("--node 前需要 --file")
			}
			selections[current].Names = append(selections[current].Names, name)
			return nil
		})
		if err = f.Parse(args); err != nil {
			return err
		}
		if o.Port == 0 {
			return fmt.Errorf("平铺模式需要 --port 指定起始端口")
		}
		return printInstance(m.CreateFlat(*o, selections))
	case "clone":
		f := flags(command)
		o := createFlags(f, "")
		from := f.String("from", "", "原实例名称或 ID")
		if err = f.Parse(args); err != nil {
			return err
		}
		return printInstance(m.Clone(*from, *o))
	case "start", "stop", "restart", "remove", "logs", "groups":
		id, err := oneArg(args)
		if err != nil {
			return err
		}
		switch command {
		case "start":
			return m.Start(id)
		case "stop":
			return m.Stop(id)
		case "restart":
			return m.Restart(id)
		case "remove":
			return m.DeleteInstance(id)
		case "logs":
			content, err := m.Logs(id)
			if err == nil {
				fmt.Println(content)
			}
			return err
		default:
			groups, err := m.Groups(id)
			if err != nil {
				return err
			}
			return printJSON(groups)
		}
	case "switch", "select", "mode", "backend", "autostart", "export":
		f := flags(command)
		id := f.String("instance", "", "实例名称或 ID")
		value := f.String("value", "", "mode/backend 的值")
		sub := f.String("sub", "", "订阅名称或 ID")
		group := f.String("group", "", "策略组")
		node := f.String("node", "", "节点")
		enabled := f.Bool("enable", true, "开启自启；--enable=false 关闭")
		out := f.String("out", "", "导出文件（不覆盖）")
		if err = f.Parse(args); err != nil {
			return err
		}
		switch command {
		case "switch":
			return m.SwitchSubscription(*id, *sub)
		case "select":
			return m.SelectNode(*id, *group, *node)
		case "mode":
			return m.SetMode(*id, *value)
		case "backend":
			return m.SetBackend(*id, *value)
		case "autostart":
			return m.SetAutostart(*id, *enabled)
		default:
			return m.ExportFlat(*id, *out)
		}
	default:
		return fmt.Errorf("未知命令 %s；运行 clashctl help 查看用法", command)
	}
}
func subscriptions(m *manager.Manager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法：clashctl sub add|update|remove|list")
	}
	command := args[0]
	args = args[1:]
	if command == "list" {
		status, err := m.Status()
		if err != nil {
			return err
		}
		return printJSON(status.Subscriptions)
	}
	if command == "add" {
		f := flags("sub add")
		name := f.String("name", "", "订阅名称")
		url := f.String("url", "", "Clash YAML 链接")
		if err := f.Parse(args); err != nil {
			return err
		}
		sub, err := m.AddSubscription(*name, *url)
		if err != nil {
			return err
		}
		sub.URL = ""
		return printJSON(sub)
	}
	id, err := oneArg(args)
	if err != nil {
		return err
	}
	if command == "update" {
		return m.RefreshSubscription(id)
	}
	if command == "remove" {
		return m.DeleteSubscription(id)
	}
	return fmt.Errorf("未知订阅命令")
}
func usage() {
	fmt.Print(`clashctl — 并行 mihomo 管理

clashctl [--home DIR]             打开 TUI
clashctl version                 查看版本
clashctl install-core            安装锁定版本 mihomo
clashctl status                  查看全部状态（JSON，隐藏密钥和订阅 URL）

clashctl sub add --name A --url URL
clashctl sub list|update|remove [NAME]
clashctl new --name work --sub A [--port 17890] [--backend nohup|user|system]
clashctl clone --from work --name work-2 [--port PORT]
clashctl start|stop|restart|remove|logs|groups INSTANCE
clashctl switch --instance work --sub B
clashctl select --instance work --group PROXY --node NODE
clashctl mode --instance work --value rule|global|direct
clashctl backend --instance work --value nohup|user|system
clashctl autostart --instance work [--enable=false]

clashctl flat --name fixed --port 20000 --file a.yaml --node NODE1 --node NODE2 --file b.yaml --node NODE3
clashctl export --instance fixed --out fixed.yaml

安装目录默认 ~/.local/clash_mihomo；安装到自定义目录后自动从二进制路径定位。
TUI: a 订阅 · n 新建 · f 平铺 · c 克隆 · p 节点 · v 切订阅 · b 后台 · o 自启。
系统服务需要 sudo；用户服务的开机自启还需用户 linger（sudo loginctl enable-linger USER）。
`)
}
