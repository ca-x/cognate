package secretservice

import (
	"os"
	"sync"
)

type State uint8

const (
	Unknown State = iota
	Found
	Missing
	Locked
)

type Secret struct {
	payload *secretPayload
}

type secretPayload struct {
	mutex sync.Mutex
	data  *[]byte
}

func (secret Secret) Copy() []byte {
	if secret.payload == nil || secret.payload.data == nil {
		return nil
	}
	secret.payload.mutex.Lock()
	defer secret.payload.mutex.Unlock()
	return append([]byte(nil), (*secret.payload.data)...)
}

func (secret Secret) Destroy() {
	if secret.payload != nil && secret.payload.data != nil {
		secret.payload.mutex.Lock()
		defer secret.payload.mutex.Unlock()
		clear(*secret.payload.data)
		*secret.payload.data = nil
	}
}

type Result struct {
	State  State
	Secret Secret
}

func (result Result) AllowFileFallback() bool {
	return result.State == Missing
}

const helperMode = "--cognate-private-secret-read-v1"

type Key struct {
	Socket  string
	Service string
	Account string
}

func HelperDispatch() (int, bool) {
	if len(os.Args) < 2 || os.Args[1] != helperMode {
		return 0, false
	}
	return serveProcess(), true
}
