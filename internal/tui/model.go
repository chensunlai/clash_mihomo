package tui

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/chensunlai/clash_mihomo/internal/manager"
)

var (
	accent        = lipgloss.Color("#7DD3FC")
	muted         = lipgloss.Color("#94A3B8")
	green         = lipgloss.Color("#86EFAC")
	purple        = lipgloss.Color("#C4B5FD")
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(accent)
	dimStyle      = lipgloss.NewStyle().Foreground(muted)
	selectedStyle = lipgloss.NewStyle().Background(lipgloss.Color("#1E293B")).Foreground(accent).Bold(true)
	panelStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#334155")).Padding(1, 2)
)

type statusMsg struct {
	status manager.Status
	err    error
}
type tickMsg time.Time
type operationMsg struct {
	label string
	value any
	err   error
}
type authorizedMsg struct {
	label string
	fn    func() (any, error)
	err   error
}
type field struct {
	label   string
	input   textinput.Model
	choices []string
	values  []string
	choice  int
}
type form struct {
	kind   string
	title  string
	fields []field
	focus  int
	target string
}
type pickItem struct {
	label string
	value string
}
type picker struct {
	kind     string
	title    string
	items    []pickItem
	cursor   int
	filter   textinput.Model
	selected map[string]bool
	target   string
	group    string
	path     string
}
type flatWizard struct {
	options    manager.CreateOptions
	selections []manager.Selection
	labels     map[string]string
}
type Model struct {
	manager     *manager.Manager
	status      manager.Status
	page        int
	cursor      int
	width       int
	height      int
	message     string
	failed      bool
	pending     int
	form        *form
	picker      *picker
	wizard      *flatWizard
	logs        *viewport.Model
	confirm     string
	confirmKind string
	refreshing  bool
}

func New(m *manager.Manager) Model {
	return Model{manager: m, width: 100, height: 30, message: "欢迎使用 · a 添加订阅，n 创建实例，f 创建固定平铺"}
}
func Run(m *manager.Manager) error {
	_, err := tea.NewProgram(New(m), tea.WithAltScreen()).Run()
	return err
}
func (m Model) Init() tea.Cmd { return tea.Batch(m.refresh(), tick()) }
func tick() tea.Cmd           { return tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }
func (m Model) refresh() tea.Cmd {
	return func() tea.Msg { s, err := m.manager.Status(); return statusMsg{s, err} }
}
func operation(label string, fn func() (any, error)) tea.Cmd {
	return func() tea.Msg { v, err := fn(); return operationMsg{label, v, err} }
}
func (m Model) launch(label string, system bool, fn func() (any, error)) (tea.Model, tea.Cmd) {
	m.pending++
	m.message = label + "…"
	m.failed = false
	if system && os.Geteuid() != 0 {
		return m, tea.ExecProcess(exec.Command("sudo", "-v"), func(err error) tea.Msg { return authorizedMsg{label, fn, err} })
	}
	return m, operation(label, fn)
}
func action(fn func() error) func() (any, error) { return func() (any, error) { return nil, fn() } }
func newField(label, value string) field {
	input := textinput.New()
	input.SetValue(value)
	input.CharLimit = 4096
	input.Width = 56
	input.Prompt = ""
	return field{label: label, input: input}
}
func choiceField(label string, choices, values []string, current string) field {
	f := field{label: label, choices: choices, values: values}
	for i, v := range values {
		if v == current {
			f.choice = i
		}
	}
	return f
}
func (f field) value() string {
	if len(f.values) > 0 {
		return f.values[f.choice]
	}
	return strings.TrimSpace(f.input.Value())
}
func (m *Model) openForm(kind, title, target string, fields ...field) {
	m.form = &form{kind: kind, title: title, target: target, fields: fields}
	if len(fields) > 0 && len(fields[0].choices) == 0 {
		m.form.fields[0].input.Focus()
	}
}
func (m *Model) openPicker(kind, title, target string, items []pickItem) {
	filter := textinput.New()
	filter.Placeholder = "输入筛选…"
	filter.Prompt = "⌕ "
	filter.Width = 60
	filter.Focus()
	m.picker = &picker{kind: kind, title: title, target: target, items: items, filter: filter, selected: map[string]bool{}}
}
func (p *picker) filtered() []pickItem {
	items := []pickItem{}
	filter := strings.ToLower(p.filter.Value())
	for _, item := range p.items {
		if strings.Contains(strings.ToLower(item.label), filter) {
			items = append(items, item)
		}
	}
	return items
}
func (m Model) visible() []manager.InstanceStatus {
	items := []manager.InstanceStatus{}
	for _, i := range m.status.Instances {
		if m.page != 2 || i.Kind == "flat" {
			items = append(items, i)
		}
	}
	return items
}
func (m Model) selected() (manager.InstanceStatus, bool) {
	items := m.visible()
	if m.cursor < 0 || m.cursor >= len(items) {
		return manager.InstanceStatus{}, false
	}
	return items[m.cursor], true
}
func (m Model) selectedSub() (manager.Subscription, bool) {
	if m.cursor < 0 || m.cursor >= len(m.status.Subscriptions) {
		return manager.Subscription{}, false
	}
	return m.status.Subscriptions[m.cursor], true
}
func backendField(current string) field {
	return choiceField("后台方式", []string{"nohup · 退出终端后继续运行", "systemd 用户服务", "systemd 系统服务"}, manager.Backends, current)
}
func (m *Model) subscriptionField(current string) (field, error) {
	labels, ids := []string{}, []string{}
	for _, s := range m.status.Subscriptions {
		labels = append(labels, s.Name)
		ids = append(ids, s.ID)
	}
	if len(ids) == 0 {
		return field{}, fmt.Errorf("请先添加订阅（a）")
	}
	return choiceField("订阅", labels, ids, current), nil
}
func (m *Model) openFlatSubscriptions() {
	items := []pickItem{}
	for _, subscription := range m.status.Subscriptions {
		items = append(items, pickItem{subscription.Name, subscription.ID})
	}
	if len(items) == 0 {
		m.message = "请先添加订阅；也可按 l 导入本地 YAML 文件"
		return
	}
	m.openPicker("flat-subscriptions", "选择节点来源订阅 · Enter 查看节点", "", items)
}
func (m *Model) createForm(clone bool) {
	name, target, backend, bind, kind := "", "", "nohup", "127.0.0.1", "create"
	if clone {
		item, ok := m.selected()
		if !ok {
			return
		}
		name = item.Name + "-副本"
		target = item.ID
		backend = item.Backend
		bind = item.Bind
		kind = "clone"
	}
	fields := []field{newField("实例名称", name)}
	if !clone {
		sub, err := m.subscriptionField("")
		if err != nil {
			m.message = err.Error()
			m.failed = true
			return
		}
		fields = append(fields, sub)
	}
	fields = append(fields, newField("mixed 起始端口（留空自动分配）", ""), newField("监听地址", bind), backendField(backend))
	title := "创建订阅实例"
	if clone {
		title = "克隆实例 · 配置独立，端口自动分配"
	}
	m.openForm(kind, title, target, fields...)
}
func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.logs != nil {
			m.logs.Width = max(20, msg.Width-12)
			m.logs.Height = max(5, msg.Height-14)
		}
		return m, nil
	case tickMsg:
		cmds := []tea.Cmd{tick()}
		if !m.refreshing {
			m.refreshing = true
			cmds = append(cmds, m.refresh())
		}
		return m, tea.Batch(cmds...)
	case statusMsg:
		m.refreshing = false
		if msg.err != nil {
			m.message = msg.err.Error()
			m.failed = true
			return m, nil
		}
		oldID := ""
		if item, ok := m.selected(); ok && m.page != 1 {
			oldID = item.ID
		}
		m.status = msg.status
		if oldID != "" {
			for index, i := range m.visible() {
				if i.ID == oldID {
					m.cursor = index
					break
				}
			}
		}
		m.clampCursor()
		return m, nil
	case authorizedMsg:
		if msg.err != nil {
			m.pending--
			m.message = "sudo 授权未完成，操作已取消"
			m.failed = true
			return m, nil
		}
		return m, operation(msg.label, msg.fn)
	case operationMsg:
		m.pending = max(0, m.pending-1)
		m.failed = msg.err != nil
		if msg.err != nil {
			m.message = msg.err.Error()
			return m, m.refresh()
		}
		m.message = msg.label + " · 完成"
		switch value := msg.value.(type) {
		case groupsResult:
			keys := []string{}
			for name := range value.groups {
				keys = append(keys, name)
			}
			sort.Strings(keys)
			items := []pickItem{}
			for _, name := range keys {
				group := value.groups[name]
				items = append(items, pickItem{name + "  →  " + group.Now, name})
			}
			m.openPicker("groups", "选择策略组", value.target, items)
		case nodesResult:
			items := []pickItem{}
			for _, name := range value.nodes {
				label := name
				if name == value.current {
					label = "● " + name
				}
				items = append(items, pickItem{label, name})
			}
			m.openPicker("nodes", "选择节点 · Enter 切换 / Ctrl+T 测速", value.target, items)
			m.picker.group = value.group
		case localResult:
			if m.wizard == nil || value.wizard != m.wizard {
				return m, m.refresh()
			}
			items := []pickItem{}
			for _, node := range value.nodes {
				name, _ := node["name"].(string)
				typ, _ := node["type"].(string)
				items = append(items, pickItem{name + "  ·  " + typ, name})
			}
			m.openPicker("flat-nodes", value.label+" · Space 多选 / Ctrl+A 全选 / Enter 确认", "", items)
			m.picker.path = value.path
			m.wizard.labels[value.path] = value.label
			for _, selection := range m.wizard.selections {
				if selection.Path == value.path {
					for _, name := range selection.Names {
						m.picker.selected[name] = true
					}
				}
			}
		case logResult:
			view := viewport.New(max(20, m.width-12), max(5, m.height-14))
			view.SetContent(clean(value.content))
			view.GotoBottom()
			m.logs = &view
		case delayResult:
			m.message = fmt.Sprintf("%s · %d ms", value.node, value.delay)
		}
		return m, m.refresh()
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.confirm != "" {
			return m.handleConfirm(msg)
		}
		if m.form != nil {
			return m.handleForm(msg)
		}
		if m.picker != nil {
			return m.handlePicker(msg)
		}
		if m.logs != nil {
			if msg.String() == "esc" || msg.String() == "q" {
				m.logs = nil
				return m, nil
			}
			var cmd tea.Cmd
			*m.logs, cmd = m.logs.Update(msg)
			return m, cmd
		}
		if m.wizard != nil {
			return m.handleWizard(msg)
		}
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "1", "2", "3":
			m.page = int(msg.String()[0] - '1')
			m.cursor = 0
		case "tab":
			m.page = (m.page + 1) % 3
			m.cursor = 0
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor++
			m.clampCursor()
		case "a":
			url := newField("Clash YAML 订阅链接", "")
			url.input.EchoMode = textinput.EchoPassword
			m.openForm("subscription", "添加订阅", "", newField("订阅名称", ""), url)
		case "n":
			m.createForm(false)
		case "c":
			if m.page != 1 {
				m.createForm(true)
			}
		case "f":
			m.openForm("flat", "创建固定平铺 · 一个节点一个 mixed 入口", "", newField("实例名称", ""), newField("起始端口", "20000"), newField("监听地址", "127.0.0.1"), backendField("nohup"))
		case "s", "x", "r", "b", "o", "p", "m", "l", "e", "u", "d", "v", "enter":
			return m.instanceKey(msg.String())
		case "i":
			return m.launch("安装 mihomo 核心", false, func() (any, error) { _, err := m.manager.InstallCore(); return nil, err })
		case "ctrl+r":
			return m, m.refresh()
		}
	}
	return m, nil
}
func (m *Model) clampCursor() {
	count := len(m.visible())
	if m.page == 1 {
		count = len(m.status.Subscriptions)
	}
	m.cursor = max(0, min(m.cursor, count-1))
}
func (m Model) instanceKey(key string) (tea.Model, tea.Cmd) {
	if m.page == 1 {
		s, ok := m.selectedSub()
		if !ok {
			return m, nil
		}
		if key == "u" {
			return m.launch("更新订阅 "+s.Name, false, action(func() error { return m.manager.RefreshSubscription(s.ID) }))
		}
		if key == "d" {
			m.confirm = s.ID
			m.confirmKind = "subscription"
		}
		return m, nil
	}
	i, ok := m.selected()
	if !ok {
		return m, nil
	}
	system := i.Backend == "system"
	switch key {
	case "enter":
		if i.Kind == "flat" {
			content := "PORT       NODE\n\n"
			for _, entry := range i.Entries {
				content += fmt.Sprintf("%s:%d  →  %s\n", i.Bind, entry.Port, entry.Node)
			}
			view := viewport.New(max(20, m.width-12), max(5, m.height-14))
			view.SetContent(clean(content))
			m.logs = &view
			return m, nil
		}
		return m.instanceKey("p")
	case "v":
		sub, err := m.subscriptionField(i.Subscription)
		if err != nil {
			m.message = err.Error()
			m.failed = true
		} else if i.Kind == "flat" {
			m.message = "固定平铺不关联订阅"
		} else {
			m.openForm("switch", "切换订阅 · 保留实例端口和后台方式", i.ID, sub)
		}
	case "s":
		return m.launch("启动 "+i.Name, system, action(func() error { return m.manager.Start(i.ID) }))
	case "x":
		return m.launch("停止 "+i.Name, system, action(func() error { return m.manager.Stop(i.ID) }))
	case "r":
		return m.launch("重启 "+i.Name, system, action(func() error { return m.manager.Restart(i.ID) }))
	case "b":
		m.openForm("backend", "切换后台方式（请先停止实例）", i.ID, backendField(i.Backend))
	case "o":
		return m.launch("切换自启 "+i.Name, system, action(func() error { return m.manager.SetAutostart(i.ID, !i.Autostart) }))
	case "p":
		if i.Kind == "flat" {
			m.message = "平铺端口固定绑定节点，右侧可查看映射"
			return m, nil
		}
		return m.launch("读取策略组", false, func() (any, error) { groups, err := m.manager.Groups(i.ID); return groupsResult{i.ID, groups}, err })
	case "m":
		m.openForm("mode", "切换代理模式", i.ID, choiceField("模式", []string{"规则 rule", "全局 global", "直连 direct"}, []string{"rule", "global", "direct"}, i.Mode))
	case "l":
		return m.launch("读取日志", false, func() (any, error) { content, err := m.manager.Logs(i.ID); return logResult{content}, err })
	case "e":
		m.openForm("export", "导出固定配置", i.ID, newField("导出文件（不覆盖已有文件）", ""))
	case "u":
		if i.Kind == "flat" {
			m.message = "固定快照不会更新，也不跟随订阅"
			return m, nil
		}
		return m.launch("更新关联订阅", false, action(func() error { return m.manager.RefreshSubscription(i.Subscription) }))
	case "d":
		m.confirm = i.ID
		m.confirmKind = "instance"
	}
	return m, nil
}

type groupsResult struct {
	target string
	groups map[string]manager.Proxy
}
type nodesResult struct {
	target  string
	group   string
	current string
	nodes   []string
}
type localResult struct {
	path   string
	label  string
	nodes  []manager.Node
	wizard *flatWizard
}

func (m Model) loadFlatNodes(path, label string) (tea.Model, tea.Cmd) {
	wizard := m.wizard
	return m.launch("读取 "+label+" 的节点", false, func() (any, error) {
		path, err := manager.ExpandPath(path)
		if err != nil {
			return nil, err
		}
		nodes, err := manager.LocalNodes(path)
		return localResult{path: path, label: label, nodes: nodes, wizard: wizard}, err
	})
}

type logResult struct{ content string }
type delayResult struct {
	node  string
	delay int
}

func (m Model) handleConfirm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "esc" || key.String() == "n" {
		m.confirm = ""
		return m, nil
	}
	if key.String() != "y" {
		return m, nil
	}
	id, kind := m.confirm, m.confirmKind
	m.confirm = ""
	system := false
	if i, err := m.manager.Instance(id); err == nil {
		system = i.Backend == "system"
	}
	return m.launch("删除", system, action(func() error {
		if kind == "subscription" {
			return m.manager.DeleteSubscription(id)
		}
		return m.manager.DeleteInstance(id)
	}))
}
func (m Model) handleForm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.form
	if key.String() == "esc" {
		m.form = nil
		return m, nil
	}
	if key.String() == "ctrl+s" || (key.String() == "enter" && f.focus == len(f.fields)-1) {
		return m.submitForm()
	}
	if key.String() == "tab" || key.String() == "enter" || key.String() == "shift+tab" {
		f.fields[f.focus].input.Blur()
		step := 1
		if key.String() == "shift+tab" {
			step = -1
		}
		f.focus = (f.focus + step + len(f.fields)) % len(f.fields)
		if len(f.fields[f.focus].choices) == 0 {
			return m, f.fields[f.focus].input.Focus()
		}
		return m, nil
	}
	current := &f.fields[f.focus]
	if len(current.choices) > 0 {
		switch key.String() {
		case "left", "up":
			current.choice = (current.choice - 1 + len(current.choices)) % len(current.choices)
		case "right", "down", " ":
			current.choice = (current.choice + 1) % len(current.choices)
		}
		return m, nil
	}
	var cmd tea.Cmd
	current.input, cmd = current.input.Update(key)
	return m, cmd
}
func parsePort(text string) (int, error) {
	if text == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("端口需要整数")
	}
	return n, nil
}
func (m Model) submitForm() (tea.Model, tea.Cmd) {
	f := m.form
	values := []string{}
	for _, field := range f.fields {
		values = append(values, field.value())
	}
	fail := func(err error) (tea.Model, tea.Cmd) { m.message = err.Error(); m.failed = true; return m, nil }
	switch f.kind {
	case "subscription":
		m.form = nil
		return m.launch("添加订阅", false, func() (any, error) { _, err := m.manager.AddSubscription(values[0], values[1]); return nil, err })
	case "create", "clone":
		offset := 1
		if f.kind == "create" {
			offset = 2
		}
		port, err := parsePort(values[offset])
		if err != nil {
			return fail(err)
		}
		options := manager.CreateOptions{Name: values[0], Port: port, Bind: values[offset+1], Backend: values[offset+2]}
		m.form = nil
		return m.launch("创建实例", false, func() (any, error) {
			if f.kind == "clone" {
				_, err := m.manager.Clone(f.target, options)
				return nil, err
			}
			_, err := m.manager.CreateInstance(options, values[1])
			return nil, err
		})
	case "flat":
		port, err := parsePort(values[1])
		if err != nil {
			return fail(err)
		}
		if port == 0 {
			return fail(fmt.Errorf("平铺需要指定起始端口"))
		}
		m.wizard = &flatWizard{options: manager.CreateOptions{Name: values[0], Port: port, Bind: values[2], Backend: values[3]}, labels: map[string]string{}}
		m.form = nil
		m.openFlatSubscriptions()
		return m, nil
	case "flat-file":
		path := values[0]
		m.form = nil
		return m.loadFlatNodes(path, "本地文件")
	case "backend":
		old, err := m.manager.Instance(f.target)
		if err != nil {
			return fail(err)
		}
		m.form = nil
		return m.launch("切换后台方式", old.Backend == "system", action(func() error { return m.manager.SetBackend(f.target, values[0]) }))
	case "mode":
		m.form = nil
		return m.launch("切换模式", false, action(func() error { return m.manager.SetMode(f.target, values[0]) }))
	case "switch":
		m.form = nil
		return m.launch("切换订阅", false, action(func() error { return m.manager.SwitchSubscription(f.target, values[0]) }))
	case "export":
		m.form = nil
		return m.launch("导出固定快照", false, action(func() error { return m.manager.ExportFlat(f.target, values[0]) }))
	}
	return m, nil
}
func (m Model) handlePicker(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.picker
	items := p.filtered()
	switch key.String() {
	case "esc":
		m.picker = nil
		return m, nil
	case "up":
		p.cursor = max(0, p.cursor-1)
		return m, nil
	case "down":
		p.cursor = min(max(0, len(items)-1), p.cursor+1)
		return m, nil
	case "ctrl+a":
		if p.kind == "flat-nodes" {
			all := true
			for _, i := range items {
				if !p.selected[i.value] {
					all = false
				}
			}
			for _, i := range items {
				p.selected[i.value] = !all
			}
			return m, nil
		}
	case " ":
		if p.kind == "flat-nodes" && len(items) > 0 {
			p.selected[items[p.cursor].value] = !p.selected[items[p.cursor].value]
			return m, nil
		}
	case "ctrl+t":
		if p.kind == "nodes" && len(items) > 0 {
			node := items[p.cursor].value
			return m.launch("测速", false, func() (any, error) {
				delay, err := m.manager.Delay(p.target, node)
				return delayResult{node, delay}, err
			})
		}
	case "enter":
		if len(items) == 0 {
			return m, nil
		}
		selected := items[p.cursor]
		switch p.kind {
		case "flat-subscriptions":
			m.picker = nil
			return m.loadFlatNodes(m.manager.Store.SubscriptionPath(selected.value), selected.label)
		case "groups":
			m.picker = nil
			return m.launch("读取节点", false, func() (any, error) {
				groups, err := m.manager.Groups(p.target)
				group := groups[selected.value]
				return nodesResult{p.target, selected.value, group.Now, group.All}, err
			})
		case "nodes":
			m.picker = nil
			return m.launch("切换到 "+selected.value, false, action(func() error { return m.manager.SelectNode(p.target, p.group, selected.value) }))
		case "flat-nodes":
			names := []string{}
			for _, i := range p.items {
				if p.selected[i.value] {
					names = append(names, i.value)
				}
			}
			if len(names) == 0 {
				m.message = "请先用 Space 选择节点"
				m.failed = true
				return m, nil
			}
			replaced := false
			for index, selection := range m.wizard.selections {
				if selection.Path == p.path {
					m.wizard.selections[index].Names = names
					replaced = true
					break
				}
			}
			if !replaced {
				m.wizard.selections = append(m.wizard.selections, manager.Selection{Path: p.path, Names: names})
			}
			m.picker = nil
			m.message = "节点已加入 · a 继续选择订阅，Enter 创建平铺实例"
			return m, nil
		}
	}
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(key)
	p.cursor = 0
	return m, cmd
}
func (m Model) handleWizard(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.wizard = nil
	case "a":
		m.openFlatSubscriptions()
	case "l":
		m.openForm("flat-file", "导入本地文件（可选）", "", newField("本地 YAML 文件路径", ""))
	case "backspace":
		if count := len(m.wizard.selections); count > 0 {
			m.wizard.selections = m.wizard.selections[:count-1]
		}
	case "enter":
		if len(m.wizard.selections) == 0 {
			m.message = "请先选择订阅中的节点"
			return m, nil
		}
		wizard := m.wizard
		m.wizard = nil
		return m.launch("创建固定平铺实例", false, func() (any, error) {
			_, err := m.manager.CreateFlat(wizard.options, wizard.selections)
			return nil, err
		})
	}
	return m, nil
}

// Strip terminal control sequences supplied by remote node names and logs.
func clean(s string) string {
	var out strings.Builder
	for _, c := range s {
		if c == '\n' || c == '\t' || (c >= 32 && c != 127 && c != 0x1b) {
			out.WriteRune(c)
		}
	}
	return out.String()
}
