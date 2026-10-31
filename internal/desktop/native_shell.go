package desktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

var nativePages = [...]struct{ label, heading string }{
	{"公开目录", "发现与安装技能"},
	{"Pack 订阅", "管理订阅与同步"},
	{"Magpie 导入", "导入服务端仓库"},
	{"账号", "账号与本机设置"},
	{"AI 配置", "AI 配置工作区"},
	{"Grok Build", "Grok Build 资产"},
}

func nativeShellTheme(base *ui.Theme, highContrast bool) (ui.Theme, ui.Color) {
	theme := *base
	panel := ui.Hex("#ffffff")
	if theme.Dark {
		panel = ui.Hex("#182332")
	}
	if highContrast {
		return theme, theme.Background
	}
	theme.Background = ui.Hex("#eef2f7")
	theme.Surface = ui.Hex("#f6f8fb")
	theme.SurfaceHover = ui.Hex("#e9eef5")
	theme.SurfacePressed = ui.Hex("#dde5ef")
	theme.Border = ui.Hex("#d2dce8")
	theme.Text = ui.Hex("#1b293c")
	theme.TextMuted = ui.Hex("#52647a")
	if theme.Dark {
		theme.Background = ui.Hex("#101823")
		theme.Surface = ui.Hex("#202e40")
		theme.SurfaceHover = ui.Hex("#2a3a50")
		theme.SurfacePressed = ui.Hex("#34465e")
		theme.Border = ui.Hex("#36485e")
		theme.Text = ui.Hex("#edf2f9")
		theme.TextMuted = ui.Hex("#b0bfd2")
	}
	return theme, panel
}

func (model *Model) nativeIdentity() string {
	if model.session.value() == "" {
		return "未登录 · 公开访问"
	}
	if model.Username == "" {
		return "已登录 · 内存会话"
	}
	return "已登录 · " + model.Username
}

func (model *Model) nativeActivity() string {
	switch {
	case model.Busy:
		return "任务进行中"
	case model.WatchStopping:
		return "后台同步停止中"
	case model.WatchActive:
		return "后台同步已启用"
	default:
		return "无前台任务"
	}
}

func (model *Model) nativeShell(context *ui.Context, content func()) {
	theme, panel := nativeShellTheme(context.Theme(), context.Preferences().HighContrast)
	context.SetTheme(&theme)
	ui.Column(context).Fill().Padding(theme.Space(5)).Gap(theme.Space(3)).Children(func() {
		ui.Row(context).Gap(theme.Space(3)).AlignItems(ui.Center).Children(func() {
			if model.Logo != nil {
				ui.Image(context, model.Logo).Width(32).Height(32)
			}
			ui.Column(context).Grow(1).MinWidth(0).Gap(theme.Space(1)).Children(func() {
				ui.Text(context, "Cognate").FontSize(theme.Rem(1.7)).Bold()
				ui.Text(context, "原生客户端 · 仅连接你的 Cognate 服务").FontSize(theme.Rem(0.9)).TextColor(theme.TextMuted).SingleLine()
			})
			ui.Column(context).MaxWidthPercent(40).Gap(theme.Space(1)).Children(func() {
				ui.Text(context, "账号身份").FontSize(theme.Rem(0.85)).TextColor(theme.TextMuted)
				ui.Text(context, model.nativeIdentity()).SingleLine().Bold()
			})
		})
		ui.Column(context).FillWidth().Gap(theme.Space(1)).Children(func() {
			ui.Text(context, "服务端连接").FontSize(theme.Rem(0.9)).Bold().TextColor(theme.TextMuted)
			before := model.Origin
			ui.TextInput(context.Key("origin"), &model.Origin).Label("服务地址").Placeholder("https://skills.example.com").FillWidth()
			if model.Origin != before {
				model.SetOrigin(model.Origin)
			}
		})
		ui.Row(context).FillWidth().Wrap().Gap(theme.Space(1.5)).Children(func() {
			for index, page := range nativePages {
				key := context.Key(fmt.Sprintf("page-%d", index))
				var button ui.Element
				if model.Page == index {
					button = ui.PrimaryButton(key, page.label).Bold()
				} else {
					button = ui.Button(key, page.label)
				}
				if button.Selected(model.Page == index).Clicked() {
					model.configurationNavigate(index)
				}
			}
		})
		ui.Column(context).FillWidth().Grow(1).MinHeight(0).Padding(theme.Space(4), theme.Space(4), 0).Gap(theme.Space(3)).Background(panel).Border(1, theme.Border).Radius(theme.Radius).Children(func() {
			if model.Page >= 0 && model.Page < len(nativePages) {
				ui.Text(context, nativePages[model.Page].heading).FontSize(theme.Rem(1.45)).Bold()
			}
			if model.Page == 1 {
				ui.Scroll(context.Key("native-pack-scroll")).Grow(1).MinHeight(0).Children(func() {
					ui.Column(context).FillWidth().Gap(theme.Space(3)).Children(content)
				})
			} else {
				content()
			}
		})
		model.nativeStatus(context, &theme, panel)
	})
}

func (model *Model) nativeStatus(context *ui.Context, theme *ui.Theme, panel ui.Color) {
	active := model.Busy || model.WatchActive || model.WatchStopping
	background := panel
	if active {
		background = panel.Mix(theme.Accent, 0.08)
	}
	preferences := context.Preferences()
	bar := ui.Row(context).FillWidth().Gap(theme.Space(3)).AlignItems(ui.Center).PaddingX(theme.Space(3)).Background(panel).Border(1, theme.Border).Radius(theme.Radius)
	bar.Children(func() {
		activity := ui.Text(context.Key(fmt.Sprintf("native-activity-%t-%t", theme.Dark, preferences.HighContrast)), model.nativeActivity()).FontSize(theme.Rem(0.85)).Bold().TextColor(theme.TextMuted).SingleLine().Shrink(0).Padding(theme.Space(0.75), theme.Space(1.5)).Background(background).Radius(theme.Radius)
		if !preferences.ReduceMotion {
			activity.Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
		}
		ui.Column(context).Grow(1).MinWidth(0).Gap(theme.Space(1)).Children(func() {
			ui.Text(context, model.Status)
			if model.Page != 1 && model.watchLocksLocal() {
				ui.Text(context, model.WatchStatus).TextColor(theme.TextMuted)
			}
		})
		if ui.Button(context.Key("cancel"), "取消任务").Disabled(!active).Clicked() {
			model.Cancel()
		}
	})
}
