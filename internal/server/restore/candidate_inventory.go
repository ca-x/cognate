package restore

import (
	"bytes"
	"context"
	"encoding/json/v2"
)

func candidateArchiveExpectations(ctx context.Context, inventory []byte, maximum uint64) (map[string][]archiveRepositoryExpectation, error) {
	_, tail, found := bytes.Cut(inventory, []byte{'\n'})
	if !found || maximum > 1000000 {
		return nil, ErrCandidateInvalid
	}
	type repositoryExpectation struct {
		hash     string
		expected archiveRepositoryExpectation
	}
	repositories := make(map[int64]*repositoryExpectation)
	var records uint64
	for len(tail) != 0 {
		if err := ctx.Err(); err != nil {
			return nil, candidateError(err)
		}
		line, remaining, found := bytes.Cut(tail, []byte{'\n'})
		if !found || records >= maximum {
			return nil, ErrCandidateInvalid
		}
		var row struct {
			Kind        string `json:"kind"`
			ID          int64  `json:"id"`
			Repository  int64  `json:"repository"`
			ArchiveHash string `json:"archive_hash"`
			SkillCount  int64  `json:"skill_count"`
			Path        string `json:"path"`
			ContentHash string `json:"content_hash"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, ErrCandidateInvalid
		}
		switch row.Kind {
		case "repository":
			if row.ID <= 0 || repositories[row.ID] != nil || row.SkillCount <= 0 {
				return nil, ErrCandidateInvalid
			}
			repositories[row.ID] = &repositoryExpectation{row.ArchiveHash, archiveRepositoryExpectation{skillCount: row.SkillCount, skills: make(map[string]string)}}
		case "skill":
			repository := repositories[row.Repository]
			if repository == nil || row.Path == "" || repository.expected.skills[row.Path] != "" {
				return nil, ErrCandidateInvalid
			}
			repository.expected.skills[row.Path] = row.ContentHash
		}
		records++
		tail = remaining
	}
	if records != maximum {
		return nil, ErrCandidateInvalid
	}
	result := make(map[string][]archiveRepositoryExpectation)
	for _, repository := range repositories {
		if int64(len(repository.expected.skills)) != repository.expected.skillCount {
			return nil, ErrCandidateInvalid
		}
		result[repository.hash] = append(result[repository.hash], repository.expected)
	}
	return result, nil
}
