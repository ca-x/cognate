package configapply

import "strings"

type bundleFamily uint8

const (
	bundleCodex bundleFamily = iota + 1
	bundleGemini
)

var geminiRoleNames = [...]string{".env", "settings.json"}

func bundleName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}

func bundleRole(family bundleFamily, index int) (string, bool) {
	switch family {
	case bundleCodex:
		if index >= 0 && index < len(codexRoleNames) {
			return codexRoleNames[index], true
		}
	case bundleGemini:
		if index >= 0 && index < len(geminiRoleNames) {
			return geminiRoleNames[index], true
		}
	}
	return "", false
}

func bundlePublishRole(target *nativeDirectory, family bundleFamily, index int, stageName string, before, after, staged nativeImage) error {
	return bundlePublishRoleAdmitted(target, family, index, stageName, before, after, staged, nil)
}

func bundlePublishRoleAdmitted(target *nativeDirectory, family bundleFamily, index int, stageName string, before, after, staged nativeImage, admit func() error) error {
	name, valid := bundleRole(family, index)
	if !valid || target == nil || !bundleName(stageName) || stageName == name || family == bundleCodex && index == 1 {
		return ErrCodexInvalid
	}
	if after.Present {
		stage, body, err := target.read(stageName, codexRoleLimit)
		clear(body)
		if err != nil || !stage.Present || stage != staged || stage != after {
			return ErrCodexAttention
		}
		if admit != nil {
			if err := admit(); err != nil {
				return err
			}
		}
		if before.Present {
			if err := target.exchange(stageName, name); err != nil {
				return err
			}
			displaced, body, err := target.read(stageName, codexRoleLimit)
			clear(body)
			if err != nil || displaced != before {
				return ErrCodexUnknown
			}
		} else if err := target.rename(stageName, target, name); err != nil {
			return err
		}
	} else {
		if !before.Present {
			return ErrCodexInvalid
		}
		if admit != nil {
			if err := admit(); err != nil {
				return err
			}
		}
		if err := target.rename(name, target, stageName); err != nil {
			return err
		}
		displaced, body, err := target.read(stageName, codexRoleLimit)
		clear(body)
		if err != nil || displaced != before {
			return ErrCodexUnknown
		}
	}
	return nil
}

func bundleActualImages(target *nativeDirectory, family bundleFamily) ([3]nativeImage, error) {
	var images [3]nativeImage
	count := 3
	if family == bundleGemini {
		count = 2
	} else if family != bundleCodex {
		return images, ErrCodexInvalid
	}
	for index := range count {
		name, valid := bundleRole(family, index)
		if !valid {
			return images, ErrCodexInvalid
		}
		image, body, err := target.read(name, codexRoleLimit)
		clear(body)
		if err != nil {
			return images, err
		}
		images[index] = image
	}
	return images, nil
}

func bundleValidateMix(before, after, actual [3]nativeImage, writes [3]bool) error {
	for index := range actual {
		if actual[index] != before[index] && (!writes[index] || actual[index] != after[index]) {
			return ErrCodexConflict
		}
	}
	return nil
}
