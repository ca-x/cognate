package library

import (
	"encoding/hex"
	"errors"
)

var ErrArchiveRepairUncertain = errors.New("archive repair not confirmed; preserve artifacts and run cache verify before another repair")

type ArchiveRepairResult struct {
	AlreadyVerified bool
	Retained        bool
}

func validArchiveRepairID(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16 && hex.EncodeToString(decoded) == value
}
