package desktop

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/ca-x/cognate/internal/packattachments"
	"github.com/egoist/mygo/ui"
)

type packConfigurationState struct {
	Catalog                       packConfigurationCatalog
	Connection                    *packConfigurationConnection
	Pack, Candidate, Confirmation string
	Snapshot                      packattachments.Snapshot
	Draft                         []packattachments.Reference
	Review                        *packattachments.Review
	Generation                    uint64
	Loaded, Editing, Blocked      bool
	Selected, AssetSelected       int
	ReviewScroll                  ui.ScrollState
}

func (model *Model) packConfigurationState() *packConfigurationState {
	if model.packConfiguration == nil {
		state := &packConfigurationState{Selected: -1, AssetSelected: -1}
		model.packConfiguration = func() *packConfigurationState { return state }
	}
	return model.packConfiguration()
}

func packConfigurationReferences(snapshot packattachments.Snapshot) []packattachments.Reference {
	refs := make([]packattachments.Reference, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		refs = append(refs, packattachments.Reference{AssetID: entry.AssetID, ExpectedAssetRevision: entry.AssetRevision, TargetID: entry.TargetID})
	}
	return refs
}

func (state *packConfigurationState) dirty() bool {
	return !slices.Equal(state.Draft, packConfigurationReferences(state.Snapshot))
}

func packConfigurationEligible(asset configurationAsset) bool {
	if asset.Kind != "provider" || asset.SchemaVersion != 1 {
		return false
	}
	return slices.Contains([]string{"claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp"}, asset.AgentID)
}

func (model *Model) packConfigurationClearReview() {
	if model.packConfiguration == nil {
		return
	}
	state := model.packConfiguration()
	if state.Review != nil && state.Connection != nil {
		state.Connection.closeReview()
	}
	state.Review, state.Confirmation = nil, ""
}

func (model *Model) packConfigurationRetire() {
	if model.packConfiguration == nil {
		return
	}
	state := model.packConfiguration()
	model.packConfigurationClearReview()
	if connection := state.Connection; connection != nil {
		state.Connection = nil
		connection.stop()
		model.workers.Go(connection.Close)
	}
	state.Blocked, state.Loaded = true, false
}

func packConfigurationMessage(err error) string {
	switch {
	case errors.Is(err, packattachments.ErrUnknown):
		return "Unknown：提交结果不明；禁止自动重放。仅显式只读核对，再重新编辑和审阅。"
	case errors.Is(err, packattachments.ErrConflict):
		return "双 CAS 或资产版本冲突；草稿保留，只读核对后重新编辑和审阅。"
	case errors.Is(err, packattachments.ErrDenied), errors.Is(err, errConfigurationIdentity):
		return "当前身份、所有权或会话不符；未授予任何配置权限。"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "已取消或超时；可能开始的写入不得重放，请只读核对。"
	case errors.Is(err, packattachments.ErrInvalid), errors.Is(err, packattachments.ErrTargetUnsupported):
		return "引用、数量、schema 或目标被拒绝；只读核对后重新审阅。"
	default:
		return "附件操作不可用；不自动重放，请显式只读核对。"
	}
}

func (model *Model) packConfigurationJob(label string, work func(context.Context) (func(), error)) {
	model.job(label, func(ctx context.Context) (func(), error) {
		commit, err := work(ctx)
		return func() {
			if err != nil {
				model.Status = packConfigurationMessage(err)
				return
			}
			if commit != nil {
				commit()
			}
		}, nil
	})
}

func (model *Model) LoadPackConfigurations() {
	state := model.packConfigurationState()
	if model.closed || model.Busy || !model.ready() || model.session.value() == "" || state.Review != nil {
		return
	}
	backend, ok := model.backend.(packConfigurationBackend)
	if !ok {
		model.Status = "所有者附件元数据不可用"
		return
	}
	preserve := state.Editing || state.Blocked && state.Snapshot.PackID != ""
	packID := state.Pack
	if preserve {
		packID = state.Snapshot.PackID
	}
	model.packConfigurationRetire()
	connection, err := backend.OpenPackConfigurations(model.origin, model.session.value())
	if err != nil {
		model.Status = packConfigurationMessage(err)
		return
	}
	state.Connection = connection
	model.packConfigurationJob("只读核实当前所有者与附件", func(ctx context.Context) (func(), error) {
		catalog, err := connection.catalog(ctx)
		if err != nil {
			return nil, err
		}
		if packID == "" && len(catalog.Packs) > 0 {
			packID = catalog.Packs[0].ID
		}
		var snapshot packattachments.Snapshot
		if packID != "" {
			if !slices.ContainsFunc(catalog.Packs, func(pack packConfigurationPack) bool { return pack.ID == packID }) {
				return nil, packattachments.ErrDenied
			}
			snapshot, err = connection.get(ctx, packID)
			if err != nil {
				return nil, err
			}
		}
		return func() {
			state.Catalog, state.Snapshot, state.Pack = catalog, snapshot, packID
			state.Loaded, state.Editing, state.Blocked = true, false, preserve
			if !preserve {
				state.Draft = packConfigurationReferences(snapshot)
				state.Selected = -1
			}
			model.Status = "已只读核实元数据；不是连接、配置授权或设备应用。"
		}, nil
	})
}

func (model *Model) BeginPackConfigurations() {
	state := model.packConfigurationState()
	if model.closed || model.Busy || !state.Loaded || state.Connection == nil || state.Review != nil || state.Snapshot.PackID == "" {
		return
	}
	state.Draft = packConfigurationReferences(state.Snapshot)
	state.Blocked, state.Editing, state.Selected = false, true, -1
	model.Status = "已明确放弃旧草稿并重新编辑；不会自动升级旧引用。"
}

func (state *packConfigurationState) editable() bool {
	return state.Loaded && state.Editing && !state.Blocked && state.Review == nil
}

func (state *packConfigurationState) add() {
	if !state.editable() || len(state.Draft) >= 16 || state.AssetSelected < 0 || state.AssetSelected >= len(state.Catalog.Assets) {
		return
	}
	asset := state.Catalog.Assets[state.AssetSelected]
	if !packConfigurationEligible(asset) {
		return
	}
	ref := packattachments.Reference{AssetID: asset.ID, ExpectedAssetRevision: asset.Revision, TargetID: asset.AgentID + ".provider.user"}
	if slices.ContainsFunc(state.Draft, func(old packattachments.Reference) bool {
		return old.AssetID == ref.AssetID && old.TargetID == ref.TargetID
	}) {
		return
	}
	state.Draft = append(state.Draft, ref)
	state.Selected = len(state.Draft) - 1
}

func (state *packConfigurationState) move(offset int) {
	index := state.Selected
	if !state.editable() || index < 0 || index >= len(state.Draft) || offset != -1 && offset != 1 || index+offset < 0 || index+offset >= len(state.Draft) {
		return
	}
	state.Draft[index], state.Draft[index+offset] = state.Draft[index+offset], state.Draft[index]
	state.Selected += offset
}

func (state *packConfigurationState) upgrade() {
	if !state.editable() || state.Selected < 0 || state.Selected >= len(state.Draft) {
		return
	}
	ref := &state.Draft[state.Selected]
	for _, asset := range state.Catalog.Assets {
		if asset.ID == ref.AssetID && packConfigurationEligible(asset) && ref.TargetID == asset.AgentID+".provider.user" {
			ref.ExpectedAssetRevision = asset.Revision
			return
		}
	}
}

func (model *Model) PreparePackConfigurations() {
	state := model.packConfigurationState()
	if model.closed || model.Busy || !state.editable() || !state.dirty() || state.Connection == nil || len(state.Draft) > 16 {
		return
	}
	for _, ref := range state.Draft {
		if !slices.ContainsFunc(state.Catalog.Assets, func(asset configurationAsset) bool {
			return asset.ID == ref.AssetID && asset.Revision == ref.ExpectedAssetRevision && packConfigurationEligible(asset) && ref.TargetID == asset.AgentID+".provider.user"
		}) {
			model.Status = "旧引用仍 stale / unknown / 缺失：需明确升级到已读版本或移除；不会有损转换。"
			return
		}
	}
	connection, before, refs := state.Connection, state.Snapshot, slices.Clone(state.Draft)
	state.Blocked = true
	model.packConfigurationJob("重验并生成 typed 双 CAS 审阅", func(ctx context.Context) (func(), error) {
		review, err := connection.prepare(ctx, before, refs)
		if err != nil {
			return nil, err
		}
		return func() {
			state.Review = &review
			state.Generation = model.generation
			state.Blocked = false
			model.Status = "请核对完整旧/新有序引用并明确确认；不是服务端预验证。"
		}, nil
	})
}

func (model *Model) SubmitPackConfigurations() {
	state := model.packConfigurationState()
	review := state.Review
	if model.closed || model.Busy || state.Blocked || review == nil || state.Confirmation != review.Confirmation {
		return
	}
	if state.Connection == nil || state.Generation != model.generation || review.Origin != model.origin || review.OwnerID != state.Catalog.OwnerID || !reflect.DeepEqual(review.Before, state.Snapshot) || !slices.Equal(review.References, state.Draft) || !time.Now().Before(review.Expires) {
		model.packConfigurationClearReview()
		state.Blocked = true
		model.Status = "审阅已失效；需只读核对与新审阅。"
		return
	}
	connection, confirmation := state.Connection, state.Confirmation
	state.Review, state.Confirmation = nil, ""
	state.Blocked, state.Editing = true, false
	model.packConfigurationJob("只提交一次完整附件双 CAS", func(ctx context.Context) (func(), error) {
		after, err := connection.submit(ctx, confirmation)
		if err != nil {
			return nil, err
		}
		return func() {
			state.Snapshot = after
			state.Draft = packConfigurationReferences(after)
			state.Blocked = false
			model.Status = fmt.Sprintf("已保存 %d 条有序附件元数据；未授权、未应用设备。", len(after.Entries))
		}, nil
	})
}
