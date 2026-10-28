package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/MoeForge/moejs"
)

const (
	maxSourceBytes   = 64 << 10
	maxJSONBytes     = 1 << 20
	maxRuntimeMemory = 16 << 20
)

func RunJSON(ctx context.Context, source, hook string, input []byte, output any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(source) == 0 || len(source) > maxSourceBytes || len(input) > maxJSONBytes || hook == "" || len(hook) > 128 || output == nil {
		return fmt.Errorf("invalid plugin invocation")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	module, err := moejs.CompileWith("plugin.mjs", source, moejs.CompileOptions{Context: ctx})
	if err != nil {
		return pluginError(ctx, "compile")
	}
	function, err := module.Hook(hook)
	if err != nil {
		return pluginError(ctx, "find hook")
	}
	runtime := moejs.NewRuntime(moejs.Options{DisableDynamicCode: true, MemoryLimit: maxRuntimeMemory})
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { runtime.Interrupt(ctx.Err()); close(interrupted) })
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
	if err := runtime.Load(module); err != nil {
		return pluginError(ctx, "load")
	}
	argument, err := runtime.ParseJSON(input)
	if err != nil {
		return pluginError(ctx, "parse input")
	}
	value, err := runtime.Call(function, argument)
	if err != nil {
		return pluginError(ctx, "run")
	}
	if state, result, isPromise := moejs.PromiseResult(value); isPromise {
		if state != moejs.PromiseFulfilled {
			return pluginError(ctx, "resolve async result")
		}
		value = result
	}
	if err := runtime.UnmarshalWith(value, output, moejs.DecodeOptions{MaxBytes: maxJSONBytes, MaxNodes: 50000}); err != nil {
		return pluginError(ctx, "decode result")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func pluginError(ctx context.Context, stage string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("plugin %s failed", stage)
}
