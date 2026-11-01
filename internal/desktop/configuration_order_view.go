package desktop

import (
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
)

func (model *Model) configurationOrderView(context *ui.Context) {
	state := model.orderState()
	locked := model.Busy || state.Blocked || state.Review != nil || !state.Loaded
	ui.Scroll(context.Key("order-page-scroll")).Grow(1).MinHeight(0).TrackScroll(&state.Scroll).Children(func() {
		ui.Column(context).FillWidth().Gap(context.Theme().Space(2)).Children(func() {
			ui.Text(context, "配置资产完整分区排序").Bold()
			ui.Text(context, "仅排列服务端 agent / 类型分区；不读取正文或密钥，不切换 provider，不应用设备。")
			if model.session.value() == "" {
				ui.Text(context, "请先在账号页登录，再显式读取私有资产分区。")
			}
			ui.Row(context).FillWidth().Wrap().Gap(8).Children(func() {
				if ui.Button(context.Key("order-load"), "重新读取 / 核实排序").Disabled(model.Busy || state.Review != nil || state.dirty() && !state.Blocked || model.session.value() == "").Clicked() {
					model.LoadConfigurationOrder()
				}
				options := make([]string, len(state.Partitions))
				for index, partition := range state.Partitions {
					options[index] = partition.label()
				}
				if len(options) > 0 && ui.Select(context.Key("order-partition"), &state.Partition, options).Label("agent / 配置类型分区").Disabled(locked || state.dirty()).Changed() {
					model.LoadConfigurationOrder()
				}
			})
			if state.Blocked {
				ui.Text(context, "本次审阅已消耗；冲突或 Unknown 不自动重放。请显式重新读取，再生成新审阅。")
			}
			if state.Loaded {
				ui.Text(context, fmt.Sprintf("完整分区 %s · %d 项 · 独立排序 revision %d", state.Partition, len(state.Draft), state.Snapshot.Revision))
				ui.Text(context, fmt.Sprintf("所选位置 %d / %d；未知 schema 仍可排序，排序不批准或修改内容。", state.Selected+1, len(state.Draft)))
			}
			state.List.Selected = &state.Selected
			state.List.Key = func(index int) any { return state.Draft[index].ID }
			state.List.Label = func(index int) string { return configurationOrderRow(index, state.Draft[index]) }
			ui.List(context.Key("order-assets"), &state.List, len(state.Draft)).Label("完整资产顺序").Disabled(locked).Height(context.Theme().Rem(12)).MinHeight(80).Rows(func(row ui.ListRow) {
				asset := state.Draft[row.Index]
				ui.Column(row.Context).FillWidth().Padding(8).Gap(4).Children(func() {
					ui.Text(row.Context, configurationOrderRow(row.Index, asset)).Bold()
					ui.Text(row.Context, fmt.Sprintf("%s · schema %d · 内容 revision %d", asset.ID, asset.SchemaVersion, asset.Revision)).TextColor(context.Theme().TextMuted)
				})
			})
			ui.Row(context).FillWidth().Wrap().Gap(8).Children(func() {
				if ui.Button(context.Key("order-up"), "上移所选资产").Disabled(locked || state.Selected <= 0).Clicked() {
					state.move(-1)
				}
				if ui.Button(context.Key("order-down"), "下移所选资产").Disabled(locked || state.Selected < 0 || state.Selected >= len(state.Draft)-1).Clicked() {
					state.move(1)
				}
				if ui.Button(context.Key("order-reset"), "放弃本地排序").Disabled(model.Busy || state.Review != nil || !state.dirty()).Clicked() {
					state.Draft = slices.Clone(state.Snapshot.Assets)
					state.Selected = -1
				}
				if ui.PrimaryButton(context.Key("order-review"), "审阅完整排序").Disabled(locked || !state.dirty()).Clicked() {
					model.PrepareConfigurationOrder()
				}
			})
		})
	})
	model.configurationOrderModal(context)
}

func configurationOrderRow(index int, asset configurationAsset) string {
	return fmt.Sprintf("%d. %s", index+1, asset.Name)
}

func (model *Model) configurationOrderModal(context *ui.Context) {
	state := model.orderState()
	review := state.Review
	if review == nil {
		return
	}
	open := true
	ui.Modal(context.Key("order-review-modal"), &open, func() {
		_, height := context.Size()
		ui.Column(context).FillWidth().Gap(8).Children(func() {
			ui.Text(context, "完整排序审阅").Bold()
			ui.Scroll(context.Key("order-review-scroll")).Height(min(float32(300), max(float32(80), height-context.Theme().Rem(24)))).TrackScroll(&state.ReviewScroll).Children(func() {
				ui.Column(context).FillWidth().Gap(6).Children(func() {
					ui.Text(context, review.Origin+" · owner "+review.OwnerID)
					ui.Text(context, fmt.Sprintf("%s · 排序 revision %d · 完整 %d 项", review.Before.label(), review.Before.Revision, len(review.IDs)))
					before := make(map[string]int, len(review.Before.Assets))
					for index, asset := range review.Before.Assets {
						before[asset.ID] = index + 1
					}
					for index, asset := range state.Draft {
						ui.Text(context, fmt.Sprintf("%d → %d · %s · %s", before[asset.ID], index+1, asset.Name, asset.ID))
					}
					ui.Text(context, "只提交本分区完整排列；不会保存内容草稿、切换 provider 或应用设备。")
				})
			})
			ui.Text(context, "精确输入："+review.Confirmation)
			ui.TextInput(context.Key("order-confirmation"), &state.Confirmation).Label("本次排序确认").Disabled(model.Busy).FillWidth()
			ui.Row(context).Wrap().Gap(8).Children(func() {
				if ui.Button(context.Key("order-deny"), "取消排序审阅").Disabled(model.Busy).Clicked() {
					model.orderClearReview()
				}
				if ui.PrimaryButton(context.Key("order-submit"), "确认并提交一次排序").Disabled(model.Busy || state.Confirmation != review.Confirmation).Clicked() {
					model.SubmitConfigurationOrder()
				}
			})
		})
	})
	if !open {
		model.orderClearReview()
	}
}
