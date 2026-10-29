package desktop

import (
	"fmt"
	"runtime"

	"github.com/egoist/mygo/ui"
)

func (model *Model) View(context *ui.Context) {
	if model.Origin != model.originInput {
		model.SetOrigin(model.Origin)
	}
	ui.Column(context).Fill().Padding(20).Gap(12).Children(func() {
		ui.Row(context).Gap(12).Children(func() {
			if model.Logo != nil {
				ui.Image(context, model.Logo).Width(40).Height(40)
			}
			ui.Text(context, "Cognate").FontSize(26).Bold()
		})
		ui.Text(context, "原生客户端 · 仅连接你的 Cognate 服务")
		before := model.Origin
		ui.TextInput(context.Key("origin"), &model.Origin).Label("服务地址").Placeholder("https://skills.example.com").FillWidth()
		if model.Origin != before {
			model.SetOrigin(model.Origin)
		}
		ui.Row(context).Gap(8).Children(func() {
			for index, title := range []string{"公开目录", "Pack 订阅", "Magpie 导入", "账号", "AI 配置"} {
				if ui.Button(context.Key(fmt.Sprintf("page-%d", index)), title).Clicked() {
					model.configurationNavigate(index)
				}
			}
		})
		switch model.Page {
		case 0:
			model.catalogView(context)
		case 1:
			model.packsView(context)
		case 2:
			model.importView(context)
		case 3:
			model.accountView(context)
		case 4:
			model.configurationView(context)
		}
		ui.Row(context).Gap(8).Children(func() {
			if ui.Button(context.Key("cancel"), "取消任务").Disabled(!model.Busy && !model.WatchActive && !model.WatchStopping).Clicked() {
				model.Cancel()
			}
			ui.Text(context, model.Status).Grow(1)
		})
		if model.Page != 1 && model.watchLocksLocal() {
			ui.Text(context, model.WatchStatus)
		}
	})
}

func (model *Model) accountView(context *ui.Context) {
	ui.Scroll(context.Key("account-scroll")).Grow(1).Children(func() {
		ui.Column(context.Key("account")).FillWidth().Gap(12).Children(func() {
			ui.Text(context, "本机设置").FontSize(20).Bold()
			ui.Text(context, "本机加密材料，与服务端登录无关；只在此明确初始化，不写入 agent 配置。")
			if ui.Button(context.Key("client-key-init"), "初始化本机客户端密钥").Disabled(runtime.GOOS != "linux" || model.Busy || model.localWorkBlocked()).Clicked() {
				model.InitializeClientKey()
			}
			if ui.Button(context.Key("account-configuration-status"), "本机配置状态 / 离线恢复").Clicked() {
				model.configurationNavigate(4)
			}
			if runtime.GOOS != "linux" {
				ui.Text(context, "本机客户端密钥初始化与本地安装当前仅支持 Linux")
			}
			if model.watchLocksLocal() {
				ui.Text(context, "请先停止后台同步并等待排空，再初始化本机密钥")
			}
			ui.Text(context, "本地账号登录").FontSize(20).Bold()
			if ui.TextInput(context.Key("username"), &model.Username).Label("用户名").Placeholder("用户名").FillWidth().Changed() {
				model.Cancel()
				model.clearOwner()
				model.Status = "账号已更改，请重新登录"
			}
			if ui.TextInput(context.Key("password"), model.password.input()).Label("密码").Placeholder("密码").Password().FillWidth().Changed() && model.Busy {
				model.Cancel()
			}
			ui.Text(context, "密码与登录会话仅保存在进程内存，不写磁盘。切换服务地址会清除会话。")
			ui.Row(context).Gap(8).Children(func() {
				if ui.PrimaryButton(context.Key("login"), "登录").Disabled(model.Busy || model.Username == "" || model.password.value() == "").Clicked() {
					model.Login()
				}
				if ui.Button(context.Key("logout"), "退出登录").Clicked() {
					model.Logout()
				}
			})
		})
	})
}

func (model *Model) catalogView(context *ui.Context) {
	ui.Row(context).Gap(8).Children(func() {
		ui.TextInput(context.Key("query"), &model.Query).Label("搜索关键词").Placeholder("搜索公开技能").Grow(1)
		if ui.PrimaryButton(context.Key("search"), "搜索").Disabled(model.Busy).Clicked() {
			model.Search(false)
		}
		if ui.Button(context.Key("next"), "下一页").Disabled(model.Busy || model.Cursor == "").Clicked() {
			model.Search(true)
		}
	})
	model.skillList.Selected = &model.Selected
	model.skillList.Key = func(index int) any { return model.Skills[index].Id }
	model.skillList.Label = func(index int) string { return model.Skills[index].Name }
	list := ui.List(context.Key("skills"), &model.skillList, len(model.Skills)).Label("公开技能").Grow(1).MinHeight(100).Rows(func(row ui.ListRow) {
		entry := model.Skills[row.Index]
		ui.Column(row.Context).Padding(8).Gap(4).Children(func() { ui.Text(row.Context, entry.Name).Bold(); ui.Text(row.Context, entry.RepositoryName) })
	})
	if list.Changed() {
		model.Select(model.Selected)
	}
	ui.Row(context).Gap(8).Children(func() {
		if ui.Button(context.Key("details"), "查看详情").Disabled(model.Busy || model.Selected < 0).Clicked() {
			model.Details()
		}
		if ui.PrimaryButton(context.Key("install"), "安装已确认技能").Disabled(model.Busy || model.localWorkBlocked() || model.Detail == nil || !model.Confirm).Clicked() {
			model.Install()
		}
	})
	if model.Detail != nil {
		entry := model.Detail
		ui.Scroll(context.Key("details-scroll")).Height(120).Children(func() {
			ui.Column(context).Gap(4).Children(func() {
				ui.Text(context, entry.Name+" · "+entry.Slug).Bold()
				ui.Text(context, entry.Description)
				ui.Text(context, entry.RepositoryName+" / "+entry.Path+" @ "+entry.Branch)
				ui.Text(context, "SHA256: "+entry.ContentHash)
			})
		})
		ui.Checkbox(context.Key("confirm"), &model.Confirm, "确认下载完整技能内容并写入本地 ~/.agents/skills 与已适配 agent 目录")
	}
}

func (model *Model) packsView(context *ui.Context) {
	ui.Text(context, "分享令牌以加密形式由共用订阅内核保存；界面和日志不会显示令牌。")
	ui.TextInput(context.Key("share-token"), model.shareToken.input()).Label("分享令牌").Placeholder("分享令牌（安全输入）").Password().FillWidth()
	ui.Row(context).Gap(8).Children(func() {
		for _, action := range []struct{ key, title string }{{"list", "刷新订阅"}, {"subscribe", "保存订阅"}, {"sync", "同步并应用安装变更"}, {"remove", "移除所选订阅"}} {
			disabled := model.Busy || model.localWorkBlocked() || action.key == "subscribe" && model.shareToken.value() == "" || action.key == "remove" && model.PackSelected < 0
			if ui.Button(context.Key("pack-"+action.key), action.title).Disabled(disabled).Clicked() {
				model.Packs(action.key)
			}
		}
	})
	ui.Text(context, "保存/移除只改订阅；同步全部订阅会安装、更新或备份移除技能。")
	ui.Text(context, "后台默认关闭；启用即首轮，每 5 分钟同步全部订阅（可跨服务），单轮 2 分钟。")
	ui.Text(context, "后台自动更新/备份移除；本地修改或失权时保留。手动操作需先停止并排空。")
	ui.Row(context).Gap(8).Children(func() {
		if ui.Button(context.Key("watch-start"), "启用后台同步").Disabled(model.Busy || model.localWorkBlocked() || model.origin == "" || runtime.GOOS != "linux").Clicked() {
			model.StartWatch()
		}
		if ui.Button(context.Key("watch-stop"), "停止后台同步").Disabled(!model.WatchActive).Clicked() {
			model.StopWatch()
		}
	})
	if runtime.GOOS != "linux" {
		ui.Text(context, "后台同步/本地安装当前仅支持 Linux")
	}
	ui.Text(context, model.WatchStatus)
	model.packList.Selected = &model.PackSelected
	model.packList.Key = func(index int) any { return model.Subscriptions[index].ID }
	model.packList.Label = func(index int) string { return model.Subscriptions[index].Name }
	ui.List(context.Key("packs"), &model.packList, len(model.Subscriptions)).Label("Pack 订阅列表").Grow(1).Rows(func(row ui.ListRow) {
		record := model.Subscriptions[row.Index]
		ui.Column(row.Context).Padding(8).Gap(4).Children(func() {
			ui.Text(row.Context, record.Name).Bold()
			ui.Text(row.Context, fmt.Sprintf("%s · 已应用 %d / 目标 %d · 启用 %t", record.Server, record.AppliedRevision, record.DesiredRevision, record.Enabled))
		})
	})
}

func (model *Model) importView(context *ui.Context) {
	ui.Text(context, "Magpie 文件导入服务端仓库；不会直接安装到本机。")
	ui.TextInput(context.Key("file"), &model.FilePath).Label("Magpie 导出路径").Placeholder("Magpie 导出 JSON 文件绝对路径").FillWidth()
	ui.Row(context).Gap(8).Children(func() {
		if ui.Button(context.Key("preview"), "预览文件").Disabled(model.Busy).Clicked() {
			model.Preview()
		}
		if ui.PrimaryButton(context.Key("apply"), "应用明确选择的仓库").Disabled(model.Busy || model.preview == nil).Clicked() {
			model.Apply()
		}
	})
	ui.Scroll(context.Key("candidates")).Grow(1).Children(func() {
		ui.Column(context).Gap(8).Children(func() {
			for _, result := range model.ImportResults {
				ui.Text(context, result)
			}
			for index, candidate := range model.Candidates {
				ui.Checkbox(context.Key(fmt.Sprintf("candidate-%d", candidate.Index)), &model.ImportSelected[index], fmt.Sprintf("%s @ %s（已存在 %t）", candidate.Repository, candidate.Branch, candidate.Existing))
			}
		})
	})
}
