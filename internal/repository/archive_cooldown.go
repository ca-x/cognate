package repository

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type archiveCooldown struct {
	mutex     sync.Mutex
	until     time.Time
	permanent bool
}

func (cooldown *archiveCooldown) admit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cooldown.mutex.Lock()
	defer cooldown.mutex.Unlock()
	if cooldown.permanent || time.Now().Before(cooldown.until) {
		return ErrArchiveRateLimited
	}
	return nil
}

func (cooldown *archiveCooldown) observe(header string) {
	header = strings.Trim(header, " \t")
	numeric := header != ""
	for _, character := range header {
		if character < '0' || character > '9' {
			numeric = false
			break
		}
	}
	var until time.Time
	var permanent bool
	if numeric {
		seconds, err := strconv.ParseUint(header, 10, 64)
		if err != nil || seconds > uint64(math.MaxInt64/int64(time.Second)) {
			permanent = true
		} else {
			until = time.Now().Add(time.Duration(seconds) * time.Second)
		}
	} else {
		var err error
		until, err = http.ParseTime(header)
		if err != nil {
			return
		}
	}
	cooldown.mutex.Lock()
	defer cooldown.mutex.Unlock()
	if permanent {
		cooldown.permanent = true
	}
	if until.After(cooldown.until) {
		cooldown.until = until
	}
}
