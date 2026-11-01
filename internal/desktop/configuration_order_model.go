package desktop

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
	"uuid"

	"github.com/egoist/mygo/ui"
)

type configurationOrderBackend interface {
	ConfigurationIdentity(context.Context, string, string) (configurationIdentity, error)
	ConfigurationAssets(context.Context, configurationIdentity) ([]configurationAsset, error)
	ConfigurationOrder(context.Context, configurationIdentity, configurationOrderPartition) (configurationOrderSnapshot, error)
	ReplaceConfigurationOrder(context.Context, configurationIdentity, configurationOrderSnapshot, []string) (configurationOrderSnapshot, error)
}

type configurationOrderReview struct {
	Origin, OwnerID, Confirmation string
	Before                        configurationOrderSnapshot
	IDs                           []string
	Generation                    uint64
}

type configurationOrderState struct {
	Origin, OwnerID, Partition, Confirmation string
	Partitions                               []configurationOrderPartition
	Snapshot                                 configurationOrderSnapshot
	Draft                                    []configurationAsset
	Loaded, Blocked                          bool
	Review                                   *configurationOrderReview
	Selected                                 int
	List                                     ui.ListState
	Scroll, ReviewScroll                     ui.ScrollState
}

func (model *Model) orderState() *configurationOrderState {
	if model.order == nil {
		state := &configurationOrderState{Selected: -1}
		model.order = func() *configurationOrderState { return state }
	}
	return model.order()
}

func (model *Model) orderClearReview() {
	if model.order != nil {
		state := model.order()
		state.Review, state.Confirmation = nil, ""
	}
}

func (state *configurationOrderState) dirty() bool {
	return !slices.Equal(configurationOrderIDs(state.Snapshot.Assets), configurationOrderIDs(state.Draft))
}

func (state *configurationOrderState) move(offset int) {
	destination := state.Selected + offset
	if !state.Loaded || state.Blocked || state.Review != nil || state.Selected < 0 || destination < 0 || destination >= len(state.Draft) || offset != -1 && offset != 1 {
		return
	}
	state.Draft[state.Selected], state.Draft[destination] = state.Draft[destination], state.Draft[state.Selected]
	state.Selected = destination
}

func configurationOrderPartitions(assets []configurationAsset, previous configurationOrderPartition) []configurationOrderPartition {
	unique := make(map[configurationOrderPartition]bool)
	for _, asset := range assets {
		unique[configurationOrderPartition{AgentID: asset.AgentID, Kind: asset.Kind}] = true
	}
	if previous.AgentID != "" {
		unique[previous] = true
	}
	partitions := make([]configurationOrderPartition, 0, len(unique))
	for partition := range unique {
		partitions = append(partitions, partition)
	}
	slices.SortFunc(partitions, func(first, second configurationOrderPartition) int {
		return cmp.Compare(first.label(), second.label())
	})
	return partitions
}

func (model *Model) orderJob(label string, work func(context.Context) (func(), error)) {
	model.jobWithTimeout(label, 60*time.Second, func(ctx context.Context) (func(), error) {
		commit, err := work(ctx)
		return func() {
			if commit != nil {
				commit()
			}
			if err != nil {
				model.Status = configurationMessage(err)
				if errors.Is(err, errConfigurationOrderUnknown) {
					model.Status = errConfigurationOrderUnknown.Error()
				}
				if errors.Is(err, errConfigurationIdentity) {
					model.order = nil
				}
			}
		}, nil
	})
}

func (model *Model) LoadConfigurationOrder() {
	if model.closed || model.Busy || !model.ready() || model.session.value() == "" {
		return
	}
	backend, ok := model.backend.(configurationOrderBackend)
	if !ok {
		model.Status = errConfigurationUnsupported.Error()
		return
	}
	state := model.orderState()
	partition := state.Snapshot.configurationOrderPartition
	for _, candidate := range state.Partitions {
		if candidate.label() == state.Partition {
			partition = candidate
		}
	}
	state.Loaded = false
	origin, session := model.origin, model.session.value()
	model.orderJob("读取完整资产分区", func(ctx context.Context) (func(), error) {
		identity, err := backend.ConfigurationIdentity(ctx, origin, session)
		if err != nil {
			return nil, err
		}
		defer identity.clear()
		assets, err := backend.ConfigurationAssets(ctx, identity)
		if err != nil {
			return nil, err
		}
		partitions := configurationOrderPartitions(assets, partition)
		if partition.AgentID == "" && len(partitions) > 0 {
			partition = partitions[0]
		}
		var snapshot configurationOrderSnapshot
		if partition.AgentID != "" {
			snapshot, err = backend.ConfigurationOrder(ctx, identity, partition)
			if err != nil {
				return nil, err
			}
		}
		return func() {
			state.Origin, state.OwnerID = identity.Origin, identity.OwnerID
			state.Partitions, state.Partition = partitions, partition.label()
			state.Snapshot, state.Draft = snapshot, slices.Clone(snapshot.Assets)
			state.Loaded, state.Blocked, state.Selected = partition.AgentID != "", false, -1
			model.Status = fmt.Sprintf("已读取 %d 个资产分区；当前分区 %d 项，仅改服务端顺序", len(partitions), len(snapshot.Assets))
		}, nil
	})
}

func (model *Model) PrepareConfigurationOrder() {
	state := model.orderState()
	if model.closed || model.Busy || !state.Loaded || state.Blocked || !state.dirty() || state.Review != nil {
		return
	}
	before := state.Snapshot
	before.Assets = slices.Clone(before.Assets)
	state.Review = &configurationOrderReview{Origin: state.Origin, OwnerID: state.OwnerID, Before: before, IDs: configurationOrderIDs(state.Draft), Generation: model.generation, Confirmation: "保存排序 " + uuid.New().String()}
	state.Confirmation = ""
}

func (model *Model) SubmitConfigurationOrder() {
	state := model.orderState()
	review := state.Review
	if model.closed || model.Busy || state.Blocked || review == nil || state.Confirmation != review.Confirmation {
		return
	}
	if review.Generation != model.generation || review.Origin != model.origin || review.OwnerID != state.OwnerID || review.Before.configurationOrderPartition != state.Snapshot.configurationOrderPartition || review.Before.Revision != state.Snapshot.Revision || !slices.Equal(review.IDs, configurationOrderIDs(state.Draft)) {
		model.orderClearReview()
		state.Blocked = true
		model.Status = "排序审阅已失效；请显式重新读取并重新审阅"
		return
	}
	backend, ok := model.backend.(configurationOrderBackend)
	if !ok {
		return
	}
	state.Blocked = true
	session := model.session.value()
	model.orderJob("提交一次完整排序 CAS", func(ctx context.Context) (func(), error) {
		identity, err := backend.ConfigurationIdentity(ctx, review.Origin, session)
		if err != nil {
			return nil, err
		}
		defer identity.clear()
		if identity.Origin != review.Origin || identity.OwnerID != review.OwnerID {
			return nil, errConfigurationIdentity
		}
		after, err := backend.ReplaceConfigurationOrder(ctx, identity, review.Before, review.IDs)
		if err != nil {
			return nil, err
		}
		return func() {
			state.Snapshot, state.Draft = after, slices.Clone(after.Assets)
			state.Blocked = false
			model.Status = "完整排序已保存；未切换 provider、未应用设备，内容草稿未保存"
		}, nil
	})
}
