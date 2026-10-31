package backup

import (
	"bytes"
	"context"
)

func sealCopyLowerBound(ctx context.Context, count uint32, kind byte, id [32]byte, readRecord func(uint32) (sealRecord, error)) (uint32, error) {
	first, end := uint32(0), count
	for first < end {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		middle := first + (end-first)/2
		record, err := readRecord(middle)
		if err != nil {
			return 0, err
		}
		if record.kind < kind || record.kind == kind && bytes.Compare(record.id[:], id[:]) < 0 {
			first = middle + 1
		} else {
			end = middle
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return first, nil
}
