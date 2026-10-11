package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	width := max(40, m.width-4)
	header := titleStyle.Render("◈  CLASHCTL") + dimStyle.Render("  /  mihomo terminal manager")
	tabs := []string{"1  实例", "2  订阅", "3  固定平铺"}
	for i, name := range tabs {
		if i == m.page {
			tabs[i] = selectedStyle.Padding(0, 2).Render(name)
		} else {
			tabs[i] = dimStyle.Padding(0, 2).Render(name)
		}
	}
	running := 0
	for _, i := range m.status.Instances {
		if i.Running {
			running++
		}
	}
	summary := dimStyle.Render(fmt.Sprintf("%d 订阅   ·   %d 实例   ·   ", len(m.status.Subscriptions), len(m.status.Instances))) + lipgloss.NewStyle().Foreground(green).Render(fmt.Sprintf("%d 运行中", running))
	if !m.status.CoreInstalled {
		summary += "   ·   按 i 安装核心"
	}
	if m.pending > 0 {
		summary += titleStyle.Render(fmt.Sprintf("   ◌ %d 项操作进行中", m.pending))
	}
	var body string
	switch {
	case m.confirm != "":
		body = panelStyle.Width(min(74, width-6)).Render(titleStyle.Render("删除确认") + "\n\n此操作会停止并删除选中的实例及其管理配置。\n订阅被使用时不会删除。\n\n" + selectedStyle.Render(" y 确认 ") + dimStyle.Render("   n / Esc 取消"))
	case m.form != nil:
		body = m.formView(width)
	case m.picker != nil:
		body = m.pickerView(width)
	case m.logs != nil:
		body = panelStyle.Padding(0, 2).Render(titleStyle.Render("日志 / 端口映射") + "\n\n" + m.logs.View() + "\n\n" + dimStyle.Render("↑↓ / PgUp PgDn 滚动 · Esc 返回"))
	case m.wizard != nil:
		body = m.wizardView(width)
	default:
		body = m.mainView(width)
	}
	message := clean(m.message)
	if m.failed {
		message = lipgloss.NewStyle().Foreground(lipgloss.Color("#FCA5A5")).Render(message)
	} else {
		message = dimStyle.Render(message)
	}
	help := "a 添加订阅  n 新建  f 平铺  c 克隆  s 启动  x 停止  r 重启"
	if m.page == 1 {
		help = "a 添加订阅  u 更新订阅  d 删除  1/2/3 切页  q 退出"
	} else {
		help += "\np 节点  v 订阅  m 模式  b 后台  o 自启  l 日志  e 导出  d 删除  q 退出"
	}
	if m.form != nil {
		help = "Tab 下一项  ←→ 选择  Ctrl+S 保存  Esc 取消"
	} else if m.picker != nil {
		help = "↑↓ 选择  输入筛选  Space 多选  Ctrl+A 全选  Enter 确认  Esc 返回"
		if m.picker.kind == "flat-subscriptions" {
			help = "↑↓ 选择订阅  输入筛选  Enter 查看节点  Esc 返回"
		}
	} else if m.wizard != nil {
		help = "a 选择订阅  l 导入文件  Backspace 移除最后一组  Enter 创建  Esc 取消"
	}
	root := lipgloss.NewStyle().Background(lipgloss.Color("#0B1220")).Foreground(lipgloss.Color("#CBD5E1")).Padding(1, 2).Width(width)
	if m.confirm != "" || m.form != nil || m.picker != nil || m.logs != nil || m.wizard != nil {
		return root.Render(header + "\n\n" + body + "\n" + lipgloss.NewStyle().Width(width).MaxHeight(2).Render(message) + "\n" + dimStyle.Render(help))
	}
	return root.Render(header + "\n\n" + lipgloss.JoinHorizontal(lipgloss.Top, tabs...) + "\n\n" + summary + "\n\n" + body + "\n" + lipgloss.NewStyle().Width(width).MaxHeight(3).Render(message) + "\n\n" + dimStyle.Render(help))
}
func (m Model) mainView(width int) string {
	leftWidth := min(36, max(24, width/3))
	rightWidth := max(20, width-leftWidth-13)
	rows := []string{}
	maxRows := max(4, m.height-19)
	count := len(m.visible())
	if m.page == 1 {
		count = len(m.status.Subscriptions)
	}
	visibleRows := max(1, maxRows/3)
	start := max(0, m.cursor-visibleRows+1)
	end := min(count, start+visibleRows)
	for index := start; index < end; index++ {
		var text string
		if m.page == 1 {
			sub := m.status.Subscriptions[index]
			text = "◇ " + clean(sub.Name)
		} else {
			i := m.visible()[index]
			icon := "○ "
			if i.Running {
				icon = "● "
			}
			label := "订阅"
			if i.Kind == "flat" {
				label = "平铺"
			}
			text = icon + clean(i.Name) + "\n  " + label + " · " + i.Backend
		}
		style := dimStyle
		if index == m.cursor {
			style = selectedStyle
		}
		rows = append(rows, style.Width(leftWidth).MaxWidth(leftWidth).Render(text))
	}
	if count == 0 {
		rows = append(rows, dimStyle.Render("还没有项目\n\na  添加订阅\nn  创建实例\nf  创建平铺"))
	}
	left := panelStyle.Width(leftWidth).Height(maxRows).Render(lipgloss.NewStyle().Width(leftWidth).MaxHeight(maxRows).Render(strings.Join(rows, "\n\n")))
	details := titleStyle.Render("并行运行，自由切换") + "\n\n同一订阅可创建多个独立实例。\n固定平铺可与订阅实例同时运行。\n\n关闭 TUI 后，代理会继续在后台运行。"
	if m.page == 1 {
		if s, ok := m.selectedSub(); ok {
			used := 0
			for _, i := range m.status.Instances {
				if i.Subscription == s.ID {
					used++
				}
			}
			details = titleStyle.Render(clean(s.Name)) + "\n\n" + fmt.Sprintf("关联实例   %d\n更新时间   %s\n\n", used, s.Updated) + "u 更新缓存及所有关联实例\nn 从订阅创建独立实例\n\n订阅 URL 已隐藏。"
		}
	} else if i, ok := m.selected(); ok {
		status := "已停止"
		if i.Running {
			status = lipgloss.NewStyle().Foreground(green).Render("运行中")
		}
		auto := "关闭"
		if i.Autostart {
			auto = "开启"
		}
		details = titleStyle.Render(clean(i.Name)) + "\n\n" + fmt.Sprintf("状态   %s\n后台   %s\n自启   %s\nAPI    127.0.0.1:%d\n", status, i.Backend, auto, i.APIPort)
		if i.Kind == "flat" {
			details += "\n" + lipgloss.NewStyle().Foreground(purple).Render("固定快照 · 不跟随订阅更新") + "\n\n"
			limit := max(2, maxRows-10)
			for index, e := range i.Entries {
				if index >= limit {
					details += fmt.Sprintf("…共 %d 个入口 · Enter 查看全部", len(i.Entries))
					break
				}
				details += fmt.Sprintf("%s:%d  →  %s\n", i.Bind, e.Port, clean(e.Original))
			}
		} else {
			name := ""
			for _, s := range m.status.Subscriptions {
				if s.ID == i.Subscription {
					name = s.Name
				}
			}
			details += fmt.Sprintf("mixed  %s:%d\n模式   %s\n订阅   %s\n\n", i.Bind, i.MixedPort, i.Mode, clean(name)) + "p 切换策略组和节点\nv 切换订阅，保留入口端口\nc 克隆为独立实例"
		}
	}
	right := panelStyle.Width(rightWidth).Height(maxRows).Render(lipgloss.NewStyle().Width(rightWidth).MaxHeight(maxRows).Render(details))
	if width < 76 {
		return right
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}
func (m Model) formView(width int) string {
	f := m.form
	parts := []string{titleStyle.Render(f.title), ""}
	for index, field := range f.fields {
		label := dimStyle.Render(field.label)
		if index == f.focus {
			label = titleStyle.Render("› " + field.label)
		}
		value := field.input.View()
		if len(field.choices) > 0 {
			value = "‹ " + field.choices[field.choice] + " ›"
			if index == f.focus {
				value = selectedStyle.Render(value)
			}
		}
		parts = append(parts, label, value)
	}
	parts = append(parts, dimStyle.Render("Ctrl+S 保存 · Tab 下一项 · Esc 取消"))
	return panelStyle.Padding(0, 2).Width(min(76, width-6)).Render(strings.Join(parts, "\n"))
}
func (m Model) pickerView(width int) string {
	p := m.picker
	items := p.filtered()
	rows := []string{titleStyle.Render(p.title), "", p.filter.View(), ""}
	count := max(4, m.height-15)
	start := max(0, p.cursor-count+1)
	end := min(len(items), start+count)
	for index := start; index < end; index++ {
		item := items[index]
		label := clean(item.label)
		if p.kind == "flat-nodes" {
			if p.selected[item.value] {
				label = "☑ " + label
			} else {
				label = "☐ " + label
			}
		}
		style := dimStyle
		if index == p.cursor {
			style = selectedStyle
		}
		rows = append(rows, style.Width(min(72, width-10)).Render(label))
	}
	if len(items) == 0 {
		rows = append(rows, "没有匹配项目")
	}
	selected := 0
	for _, enabled := range p.selected {
		if enabled {
			selected++
		}
	}
	rows = append(rows, "", dimStyle.Render(fmt.Sprintf("%d 项匹配 · %d 项已选", len(items), selected)))
	return panelStyle.Padding(0, 2).Width(min(76, width-6)).Render(strings.Join(rows, "\n"))
}
func (m Model) wizardView(width int) string {
	w := m.wizard
	lines := []string{titleStyle.Render("固定平铺 · " + clean(w.options.Name)), "", fmt.Sprintf("监听 %s · 起始端口 %d · %s", w.options.Bind, w.options.Port, w.options.Backend), ""}
	port := w.options.Port
	for _, selection := range w.selections {
		label := w.labels[selection.Path]
		if label == "" || label == "本地文件" {
			label = filepath.Base(selection.Path)
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(purple).Render(clean(label)))
		for _, name := range selection.Names {
			lines = append(lines, fmt.Sprintf("  %d → %s", port, clean(name)))
			port++
		}
	}
	if len(w.selections) == 0 {
		lines = append(lines, "尚未选择节点。按 a 从已有订阅中选择。")
	}
	lines = append(lines, "", dimStyle.Render("a 继续选择订阅 · l 导入文件 · Enter 创建 · Backspace 移除最后一组 · Esc 取消"))
	return panelStyle.Padding(0, 2).Width(min(82, width-6)).Render(lipgloss.NewStyle().MaxHeight(max(6, m.height-11)).Render(strings.Join(lines, "\n")))
}
