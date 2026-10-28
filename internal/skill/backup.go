package skill

import (
	"context"
	"fmt"
	"os"
)

type BackupFingerprint struct {
	ContentHash string
	ModeHash    string
	RootMode    int
}

func BackupFingerprintRootContext(ctx context.Context, root *os.Root) (BackupFingerprint, error) {
	if err := ctx.Err(); err != nil {
		return BackupFingerprint{}, err
	}
	if root == nil {
		return BackupFingerprint{}, fmt.Errorf("backup source is unavailable")
	}
	before, err := root.Stat(".")
	if err != nil {
		return BackupFingerprint{}, fmt.Errorf("inspect backup source: %w", err)
	}
	if !before.IsDir() {
		return BackupFingerprint{}, fmt.Errorf("backup source is not a directory")
	}
	filesystem := rootFS{root: root}
	content, err := contentHashIncludingGit(ctx, filesystem, ".", true)
	if err != nil {
		return BackupFingerprint{}, err
	}
	modes, err := contentModeHash(ctx, filesystem, ".")
	if err != nil {
		return BackupFingerprint{}, err
	}
	contentAgain, err := contentHashIncludingGit(ctx, filesystem, ".", true)
	if err != nil {
		return BackupFingerprint{}, err
	}
	modesAgain, err := contentModeHash(ctx, filesystem, ".")
	if err != nil {
		return BackupFingerprint{}, err
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) || before.Mode().Perm() != after.Mode().Perm() || content != contentAgain || modes != modesAgain {
		return BackupFingerprint{}, fmt.Errorf("backup source changed during inspection")
	}
	return BackupFingerprint{ContentHash: content, ModeHash: modes, RootMode: int(before.Mode().Perm())}, nil
}
