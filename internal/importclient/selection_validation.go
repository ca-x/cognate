package importclient

import (
	"bytes"
	"encoding/hex"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/repository"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func selectionKnown(message protoreflect.Message, depth int) bool {
	if !message.IsValid() || depth > 32 || len(message.GetUnknown()) != 0 {
		return false
	}
	valid := true
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		check := func(value protoreflect.Value) bool {
			if field.Message() != nil {
				return selectionKnown(value.Message(), depth+1)
			}
			return field.Enum() == nil || field.Enum().Values().ByNumber(value.Enum()) != nil
		}
		if field.IsMap() {
			valid = false
		} else if field.IsList() {
			for index := range value.List().Len() {
				if !check(value.List().Get(index)) {
					valid = false
					break
				}
			}
		} else {
			valid = check(value)
		}
		return valid
	})
	return valid
}

func selectionText(value string, maximum int, empty bool) bool {
	return (empty || value != "") && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func selectionBindingValid(binding *importv1.SkillBinding) bool {
	if binding == nil || !selectionKnown(binding.ProtoReflect(), 0) || !selectionText(binding.RepositoryId, 128, false) || !selectionText(binding.SkillId, 128, false) || !validHash(binding.ArchiveHash) || !validHash(binding.ContentHash) || !selectionText(binding.LogicalPath, 4096, false) || strings.HasPrefix(binding.LogicalPath, "/") || strings.Contains(binding.LogicalPath, "\\") {
		return false
	}
	for component := range strings.SplitSeq(binding.LogicalPath, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	canonical, err := repository.ParseReference(binding.Repository, binding.Branch)
	return err == nil && canonical.NameWithOwner == binding.Repository && canonical.Branch == binding.Branch && selectionText(binding.Branch, 1024, true)
}

func selectionNotices(view *importv1.PreviewImportSelectionResponse) []*importv1.ImportNotice {
	notices := slices.Clone(view.Notices)
	for _, entry := range view.Skills {
		notices = append(notices, entry.Notices...)
	}
	for _, entry := range view.Configurations {
		notices = append(notices, entry.Notices...)
	}
	return notices
}

func selectionNoticesValid(notices []*importv1.ImportNotice) bool {
	if len(notices) > 2048 {
		return false
	}
	seen := make(map[string]bool)
	for _, notice := range notices {
		if notice == nil || !validHash(notice.Id) || seen[notice.Id] || notice.Code < 1 || notice.Code > 5 || notice.Count == 0 {
			return false
		}
		seen[notice.Id] = true
	}
	return true
}

func selectionDraftValid(candidate *importv1.ImportConfigurationCandidate) bool {
	draft := candidate.Draft
	if draft == nil || !selectionText(draft.Name, 128, false) || strings.TrimSpace(draft.Name) != draft.Name || draft.Name != candidate.SafeName || candidate.Kind != configurationv1.AssetKind_ASSET_KIND_PROVIDER {
		return false
	}
	switch candidate.KnownAgentId {
	case "claude":
		provider := draft.GetClaudeProvider()
		return provider != nil && provider.AuthMode >= 1 && provider.AuthMode <= 3
	case "codex":
		provider := draft.GetCodexProvider()
		return provider != nil && provider.AuthMode == configurationv1.CodexAuthMode_CODEX_AUTH_MODE_PROVIDER_KEY && provider.Endpoint != "" && provider.Model != ""
	case "gemini":
		provider := draft.GetGeminiProvider()
		if provider == nil {
			return false
		}
		return provider.AuthMode == configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_NATIVE_LOGIN && provider.Endpoint == nil && provider.KeyTransport == nil || provider.AuthMode == configurationv1.GeminiAuthMode_GEMINI_AUTH_MODE_API_KEY && provider.KeyTransport != nil && (*provider.KeyTransport == 1 || *provider.KeyTransport == 2)
	case "grokbuild":
		provider := draft.GetGrokbuildProvider()
		return provider != nil && provider.Mode == configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL && selectionGrokModelValid(provider.Model)
	}
	return false
}

func selectionGrokModelValid(model *configurationv1.GrokBuildModel) bool {
	literal := func(value string, maximum int) bool {
		return selectionText(value, maximum, false) && strings.TrimSpace(value) == value
	}
	if model == nil || !literal(model.Profile, 128) || !literal(model.UpstreamModel, 256) || !literal(model.Name, 128) || !literal(model.Endpoint, 2048) || model.ApiBackend != "responses" || model.ContextWindow < 1 || model.ContextWindow > agentconfig.MaxGrokBuildContextWindow || strings.ContainsAny(model.Endpoint, "?#\\") {
		return false
	}
	endpoint, err := url.Parse(model.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || strings.HasSuffix(endpoint.Host, ":") || strings.Contains(endpoint.Path, "\\") || !utf8.ValidString(endpoint.Path) || strings.ContainsFunc(endpoint.Path, unicode.IsControl) {
		return false
	}
	if endpoint.Port() != "" {
		port, err := strconv.Atoi(endpoint.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	if model.EnvKey != nil {
		value := *model.EnvKey
		if len(value) == 0 || len(value) > 128 {
			return false
		}
		for index, character := range []byte(value) {
			letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_'
			if !letter && (index == 0 || character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}

func selectionPreviewValid(view *importv1.PreviewImportSelectionResponse, now time.Time) bool {
	if view == nil || !selectionKnown(view.ProtoReflect(), 0) || len(view.Ticket) != 290 || view.ExpiresMs <= now.UnixMilli() || view.ExpiresMs > now.Add(5*time.Minute).UnixMilli() || len(view.Repositories) > 1000 || len(view.Skills) > 1000 || len(view.Configurations) > 34 {
		return false
	}
	seen := make(map[string]bool)
	add := func(id string) bool {
		if !validHash(id) || seen[id] {
			return false
		}
		seen[id] = true
		return true
	}
	repositories := make(map[string]bool)
	for _, entry := range view.Repositories {
		if entry == nil || !add(entry.Id) || entry.Hint == nil || (entry.Status != 1 && entry.Status != 7) {
			return false
		}
		canonical, err := repository.ParseReference(entry.Hint.Repository, entry.Hint.Branch)
		if err != nil || canonical.NameWithOwner != entry.Hint.Repository || canonical.Branch != entry.Hint.Branch || !selectionText(entry.Hint.Branch, 1024, true) {
			return false
		}
		repositories[entry.Id] = true
	}
	for _, entry := range view.Skills {
		if entry == nil || !add(entry.Id) || !slices.Contains([]importv1.ImportCandidateStatus{1, 3, 4, 5, 6, 7}, entry.Status) || !selectionText(entry.SafeName, 1024, true) {
			return false
		}
		if entry.RepositoryCandidateId != "" && !repositories[entry.RepositoryCandidateId] {
			return false
		}
		if entry.Status == 1 {
			if !selectionBindingValid(entry.Binding) {
				return false
			}
		} else if entry.Binding != nil {
			return false
		}
	}
	agents := []string{"claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp"}
	slots := map[string][]string{"claude": {"claude.api-key", "claude.auth-token"}, "codex": {"codex.provider-key"}, "gemini": {"gemini.api-key", "gemini.legacy-api-key"}, "grokbuild": {"grokbuild.api-key"}}
	for _, entry := range view.Configurations {
		if entry == nil || !add(entry.Id) || !slices.Contains([]importv1.ImportCandidateStatus{1, 2, 5, 6, 7, 8}, entry.Status) || entry.KnownAgentId != "" && !slices.Contains(agents, entry.KnownAgentId) || entry.Kind < 1 || entry.Kind > 5 || !selectionText(entry.SafeName, 1024, true) {
			return false
		}
		seenSlots := make(map[string]bool)
		for _, slot := range entry.CredentialSlots {
			if slot == nil || seenSlots[slot.SlotId] || !slices.Contains(slots[entry.KnownAgentId], slot.SlotId) {
				return false
			}
			seenSlots[slot.SlotId] = true
		}
		if entry.Status == 1 {
			if !selectionDraftValid(entry) || len(entry.CredentialSlots) != len(slots[entry.KnownAgentId]) {
				return false
			}
		} else if entry.Draft != nil {
			return false
		}
	}
	return selectionNoticesValid(selectionNotices(view))
}

func selectionRequest(view *importv1.PreviewImportSelectionResponse, choice SelectionChoice) (*importv1.CommitImportSelectionRequest, error) {
	request := &importv1.CommitImportSelectionRequest{}
	selectIDs := func(indices []int, limit, count int, get func(int) (string, importv1.ImportCandidateStatus)) ([]string, error) {
		if len(indices) > limit {
			return nil, errSelection
		}
		seen := make(map[int]bool)
		result := make([]string, 0, len(indices))
		for _, index := range indices {
			if index < 1 || index > count || seen[index] {
				return nil, errSelection
			}
			seen[index] = true
			id, status := get(index - 1)
			if status != 1 {
				return nil, errSelection
			}
			result = append(result, id)
		}
		return result, nil
	}
	var err error
	request.RepositoryIds, err = selectIDs(choice.Repositories, 16, len(view.Repositories), func(index int) (string, importv1.ImportCandidateStatus) {
		return view.Repositories[index].Id, view.Repositories[index].Status
	})
	if err != nil {
		return nil, err
	}
	request.SkillIds, err = selectIDs(choice.Skills, 128, len(view.Skills), func(index int) (string, importv1.ImportCandidateStatus) {
		return view.Skills[index].Id, view.Skills[index].Status
	})
	if err != nil {
		return nil, err
	}
	request.ConfigurationIds, err = selectIDs(choice.Configurations, 32, len(view.Configurations), func(index int) (string, importv1.ImportCandidateStatus) {
		return view.Configurations[index].Id, view.Configurations[index].Status
	})
	if err != nil {
		return nil, err
	}
	if len(request.RepositoryIds)+len(request.SkillIds)+len(request.ConfigurationIds) == 0 {
		return nil, errSelection
	}
	notices := selectionNotices(view)
	if len(choice.AcknowledgedNotices) != len(notices) {
		return nil, errSelection
	}
	for index, notice := range notices {
		if notice.Id != choice.AcknowledgedNotices[index] {
			return nil, errSelection
		}
	}
	request.AckNoticeIds = slices.Clone(choice.AcknowledgedNotices)
	return request, nil
}

func selectionReceiptShape(receipt *importv1.ImportReceipt, operation []byte) bool {
	if receipt == nil || !selectionKnown(receipt.ProtoReflect(), 0) || !bytes.Equal(receipt.OperationId, operation) || receipt.Revision <= 0 || receipt.Phase < 1 || receipt.Phase > 5 || len(receipt.RepositoryOutcomes) > 16 || len(receipt.SelectedSkillBindings) > 128 || len(receipt.CreatedAssets) > 32 || !selectionNoticesValid(receipt.Notices) {
		return false
	}
	seen := make(map[string]bool)
	for _, entry := range receipt.RepositoryOutcomes {
		if entry == nil || !validHash(entry.CandidateId) || seen[entry.CandidateId] || entry.Code < 1 || entry.Code > 8 {
			return false
		}
		seen[entry.CandidateId] = true
		if entry.Code <= 2 {
			if entry.RepositoryId == nil || !selectionText(*entry.RepositoryId, 128, false) {
				return false
			}
		} else if entry.RepositoryId != nil {
			return false
		}
	}
	for _, binding := range receipt.SelectedSkillBindings {
		if !selectionBindingValid(binding) {
			return false
		}
	}
	seen = make(map[string]bool)
	for _, asset := range receipt.CreatedAssets {
		if asset == nil || !selectionText(asset.Id, 128, false) || seen[asset.Id] || asset.Revision <= 0 || asset.SchemaVersion != 1 || asset.Kind != configurationv1.AssetKind_ASSET_KIND_PROVIDER || !slices.Contains([]string{"claude", "codex", "gemini", "grokbuild"}, asset.AgentId) || !selectionText(asset.Name, 128, false) {
			return false
		}
		seen[asset.Id] = true
	}
	if receipt.Phase <= 2 || receipt.Phase == 5 {
		return len(receipt.SelectedSkillBindings) == 0 && len(receipt.CreatedAssets) == 0
	}
	if receipt.Phase == 3 {
		for _, entry := range receipt.RepositoryOutcomes {
			if entry.Code > 2 {
				return false
			}
		}
	}
	return true
}

func selectionReceiptValid(receipt *importv1.ImportReceipt, request *importv1.CommitImportSelectionRequest, view *importv1.PreviewImportSelectionResponse) bool {
	if !selectionReceiptShape(receipt, request.OperationId) || len(receipt.RepositoryOutcomes) != len(request.RepositoryIds) {
		return false
	}
	for index, entry := range receipt.RepositoryOutcomes {
		if entry.CandidateId != request.RepositoryIds[index] {
			return false
		}
	}
	if len(receipt.CreatedAssets) != 0 && len(receipt.CreatedAssets) != len(request.ConfigurationIds) || len(receipt.SelectedSkillBindings) != 0 && len(receipt.SelectedSkillBindings) != len(request.SkillIds) {
		return false
	}
	for index, binding := range receipt.SelectedSkillBindings {
		candidate := slices.IndexFunc(view.Skills, func(entry *importv1.ImportSkillCandidate) bool { return entry.Id == request.SkillIds[index] })
		if candidate < 0 || !proto.Equal(binding, view.Skills[candidate].Binding) {
			return false
		}
	}
	for index, asset := range receipt.CreatedAssets {
		candidate := slices.IndexFunc(view.Configurations, func(entry *importv1.ImportConfigurationCandidate) bool {
			return entry.Id == request.ConfigurationIds[index]
		})
		if candidate < 0 {
			return false
		}
		expected := view.Configurations[candidate]
		if asset.AgentId != expected.KnownAgentId || asset.Name != expected.SafeName || asset.Kind != expected.Kind {
			return false
		}
	}
	if receipt.Phase == 3 && (len(receipt.CreatedAssets) != len(request.ConfigurationIds) || len(receipt.SelectedSkillBindings) != len(request.SkillIds)) {
		return false
	}
	notices := selectionNotices(view)
	for _, notice := range receipt.Notices {
		if !slices.ContainsFunc(notices, func(expected *importv1.ImportNotice) bool { return proto.Equal(notice, expected) }) {
			return false
		}
	}
	return true
}

func selectionStatusValid(response *importv1.GetImportCommitStatusResponse, operation []byte) bool {
	if response == nil || !selectionKnown(response.ProtoReflect(), 0) || response.Observation < 1 || response.Observation > 4 {
		return false
	}
	if response.Observation == 1 {
		return response.Receipt == nil
	}
	if !selectionReceiptShape(response.Receipt, operation) {
		return false
	}
	return (response.Receipt.Phase >= 3) == (response.Observation == 4)
}

func selectionResult(receipt *importv1.ImportReceipt) SelectionResult {
	observation := importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_IN_PROGRESS
	if receipt.Phase >= 3 {
		observation = importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_TERMINAL
	}
	return SelectionResult{OperationID: hex.EncodeToString(receipt.OperationId), Observation: observation, Phase: receipt.Phase, Revision: receipt.Revision, Repositories: len(receipt.RepositoryOutcomes), Skills: len(receipt.SelectedSkillBindings), Configurations: len(receipt.CreatedAssets)}
}

func selectionReceiptProgress(previous, next *importv1.ImportReceipt) bool {
	if previous == nil {
		return true
	}
	if previous.Phase >= 3 || previous.Revision == next.Revision {
		return proto.Equal(previous, next)
	}
	return next.Revision > previous.Revision
}
