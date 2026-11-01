package desktop

import (
	"fmt"
	"github.com/ca-x/cognate/internal/packattachments"
	"github.com/egoist/mygo/ui"
)

func (model *Model) packConfigurationView(context *ui.Context) {
	ui.Text(context, "所有者配置附件").Bold()
	ui.Text(context, "附件仅元数据，不授配置权限，不应用设备。")
	state := model.packConfigurationState()
	locked := model.Busy || !state.editable()
	if model.session.value() == "" {
		ui.Text(context, "请先在账号页登录；分享令牌不是配置授权。")
	}
	ui.Row(context).FillWidth().Wrap().Gap(8).Children(func() {
		if ui.Button(context.Key("pc-load"), "读取所有者附件").Disabled(model.Busy || state.Review != nil || model.session.value() == "").Clicked() {
			model.LoadPackConfigurations()
		}
		if state.Loaded && len(state.Catalog.Packs) > 0 {
			options := make([]string, len(state.Catalog.Packs))
			selected := ""
			for index, pack := range state.Catalog.Packs {
				options[index] = pack.Name + " · " + pack.ID
				if pack.ID == state.Pack {
					selected = options[index]
				}
			}
			if ui.Select(context.Key("pc-pack"), &selected, options).Label("所有者 Pack").Disabled(model.Busy || state.Review != nil || state.Editing || state.Blocked).Changed() {
				for index, option := range options {
					if selected == option {
						state.Pack = state.Catalog.Packs[index].ID
						model.LoadPackConfigurations()
						break
					}
				}
			}
		}
	})
	if state.Blocked {
		ui.Text(context, "旧草稿只读保留；不重放。显式读取后，须放弃旧草稿并重新编辑/审阅。")
	}
	if !state.Loaded {
		model.packConfigurationModal(context)
		return
	}
	ui.Text(context, state.Catalog.Origin+" · owner "+state.Catalog.OwnerID)
	for _, pack := range state.Catalog.Packs {
		if pack.ID == state.Pack {
			ui.Text(context, pack.Name).Bold()
		}
	}
	if state.Snapshot.PackID == "" {
		ui.Text(context, "当前所有者没有 Pack；不会使用订阅或匿名分享中的引用。")
		return
	}
	ui.Text(context, fmt.Sprintf("Pack %s · packRevision %d · configurationRevision %d", state.Snapshot.PackID, state.Snapshot.PackRevision, state.Snapshot.ConfigurationRevision))
	ui.Text(context, "当前服务端有序引用（含 stale / unsupported；不读取正文或秘密）")
	for index, entry := range state.Snapshot.Entries {
		ui.Text(context, fmt.Sprintf("%d. %s · agent %s · kind %d · schema %d · pinned %d · %s · state %d · NativeUnavailable", index+1, entry.AssetID, entry.AgentID, entry.Kind, entry.Schema, entry.AssetRevision, entry.TargetID, entry.State))
	}
	ui.Row(context).FillWidth().Wrap().Gap(8).Children(func() {
		if ui.Button(context.Key("pc-edit"), "放弃旧草稿，重新编辑").Disabled(model.Busy || state.Review != nil).Clicked() {
			model.BeginPackConfigurations()
		}
		if ui.Button(context.Key("pc-discard"), "放弃附件草稿").Disabled(model.Busy || state.Review != nil || !state.Editing).Clicked() {
			state.Draft = packConfigurationReferences(state.Snapshot)
			state.Editing = false
			state.Selected = -1
		}
	})
	ui.Text(context, "全部所有者资产元数据；未知 schema 只读。14 类 schema 1 provider 仅元数据目标，不代表 native 资格。")
	for index, asset := range state.Catalog.Assets {
		ui.Column(context.Key("pc-asset-" + asset.ID)).FillWidth().Gap(4).Children(func() {
			if ui.Button(context.Key("select"), asset.Name).Selected(state.AssetSelected == index).Clicked() {
				state.AssetSelected = index
			}
			ui.Text(context, fmt.Sprintf("%s · %s · %s · schema %d · current %d · 可附加 %t", asset.ID, asset.AgentID, asset.Kind, asset.SchemaVersion, asset.Revision, packConfigurationEligible(asset)))
		})
	}
	ui.Text(context, fmt.Sprintf("待提交完整有序引用 %d / 16；selected %d。旧 pin 不自动升级。", len(state.Draft), state.Selected+1))
	for index, ref := range state.Draft {
		ui.Column(context.Key("pc-ref-" + ref.AssetID + "/" + ref.TargetID)).FillWidth().Gap(4).Children(func() {
			if ui.Button(context.Key("select"), fmt.Sprintf("选择附件 %d", index+1)).Selected(state.Selected == index).Clicked() {
				state.Selected = index
			}
			ui.Text(context, packConfigurationReferenceText(index, ref))
		})
	}
	ui.Row(context).FillWidth().Wrap().Gap(8).Children(func() {
		eligible := state.AssetSelected >= 0 && state.AssetSelected < len(state.Catalog.Assets) && packConfigurationEligible(state.Catalog.Assets[state.AssetSelected])
		if ui.Button(context.Key("pc-add"), "添加所选资产引用").Disabled(locked || !eligible || len(state.Draft) >= 16).Clicked() {
			state.add()
		}
		if ui.Button(context.Key("pc-up"), "上移附件").Disabled(locked || state.Selected <= 0).Clicked() {
			state.move(-1)
		}
		if ui.Button(context.Key("pc-down"), "下移附件").Disabled(locked || state.Selected < 0 || state.Selected >= len(state.Draft)-1).Clicked() {
			state.move(1)
		}
		if ui.Button(context.Key("pc-upgrade"), "明确升级所选 pin").Disabled(locked || state.Selected < 0).Clicked() {
			state.upgrade()
		}
		if ui.Button(context.Key("pc-remove"), "移除所选引用").Disabled(locked || state.Selected < 0 || state.Selected >= len(state.Draft)).Clicked() {
			state.Draft = append(state.Draft[:state.Selected], state.Draft[state.Selected+1:]...)
			state.Selected = min(state.Selected, len(state.Draft)-1)
		}
		if ui.Button(context.Key("pc-clear"), "清空待提交引用").Disabled(locked || len(state.Draft) == 0).Clicked() {
			state.Draft = []packattachments.Reference{}
			state.Selected = -1
		}
		if ui.PrimaryButton(context.Key("pc-review"), "审阅附件替换").Disabled(locked || !state.dirty()).Clicked() {
			model.PreparePackConfigurations()
		}
	})
	model.packConfigurationModal(context)
}

func packConfigurationReferenceText(index int, ref packattachments.Reference) string {
	return fmt.Sprintf("%d. asset %s · pinned %d · target %s · NativeUnavailable", index+1, ref.AssetID, ref.ExpectedAssetRevision, ref.TargetID)
}

func (model *Model) packConfigurationModal(context *ui.Context) {
	state := model.packConfigurationState()
	review := state.Review
	if review == nil {
		return
	}
	open := true
	ui.Modal(context.Key("pc-review-modal"), &open, func() {
		_, height := context.Size()
		ui.Column(context).FillWidth().Gap(8).Children(func() {
			ui.Text(context, "完整附件元数据审阅").Bold()
			ui.Scroll(context.Key("pc-review-scroll")).Height(min(float32(300), max(float32(80), height-context.Theme().Rem(24)))).TrackScroll(&state.ReviewScroll).Children(func() {
				ui.Column(context).FillWidth().Gap(6).Children(func() {
					ui.Text(context, review.Origin+" · owner "+review.OwnerID)
					ui.Text(context, fmt.Sprintf("Pack %s · packRevision %d · configurationRevision %d · 有效至 %s", review.Before.PackID, review.Before.PackRevision, review.Before.ConfigurationRevision, review.Expires.Format("15:04:05")))
					ui.Text(context, "旧的完整有序引用")
					for index, ref := range packConfigurationReferences(review.Before) {
						ui.Text(context, packConfigurationReferenceText(index, ref))
					}
					ui.Text(context, fmt.Sprintf("新的完整有序引用 %d 项（零项明确清空）", len(review.References)))
					for index, ref := range review.References {
						ui.Text(context, packConfigurationReferenceText(index, ref))
					}
					ui.Text(context, "仅元数据/CAS 审阅，不是服务端预验证；服务端提交时复验。附件不授配置权限，不连接 MCP、不应用设备。")
				})
			})
			ui.Text(context, "精确输入："+review.Confirmation)
			ui.TextInput(context.Key("pc-confirmation"), &state.Confirmation).Label("本次附件确认").FillWidth()
			ui.Row(context).Wrap().Gap(8).Children(func() {
				if ui.Button(context.Key("pc-deny"), "取消附件审阅").Clicked() {
					model.packConfigurationClearReview()
				}
				if ui.PrimaryButton(context.Key("pc-submit"), "确认并替换一次附件").Disabled(state.Confirmation != review.Confirmation || model.Busy).Clicked() {
					model.SubmitPackConfigurations()
				}
			})
		})
	})
	if !open {
		model.packConfigurationClearReview()
	}
}
