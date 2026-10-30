//go:build !unix

package backup

import "os"

func newSealDirectory(*os.File) (*sealDirectory, error)       { return nil, ErrUnsupported }
func sealOpen(*sealDirectory, string, bool) (*os.File, error) { return nil, ErrUnsupported }
func sealBorrow(*os.File) (os.FileInfo, error)                { return nil, ErrUnsupported }
func sealRepin(*os.File, os.FileInfo) error                   { return ErrUnsupported }
func sealUnlink(*sealDirectory, string) error                 { return ErrUnsupported }
func sealCloseDirectory(*sealDirectory) error                 { return ErrUnsupported }

func discardSealDirectory(*sealDirectory) error { return ErrUnsupported }
