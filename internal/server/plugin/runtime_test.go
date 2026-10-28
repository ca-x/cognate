package plugin_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/plugin"
)

func TestRunModuleExecutesIsolatedHook(t *testing.T) {
	var result struct {
		Value int `json:"value"`
	}
	if err := plugin.RunJSON(t.Context(), `export function run(input) { return {value: input.value + 1}; }`, "run", []byte(`{"value":41}`), &result); err != nil || result.Value != 42 {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}

func TestRunModuleInterruptsLoopAndRedactsErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	var result any
	if err := plugin.RunJSON(ctx, `export function run() { for (;;) {} }`, "run", []byte(`null`), &result); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("interrupt = %v", err)
	}
	if err := plugin.RunJSON(t.Context(), `export function run() { throw Error("sensitive detail"); }`, "run", []byte(`null`), &result); err == nil || strings.Contains(err.Error(), "sensitive detail") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunModuleHasNoAmbientFilesystemOrDynamicCode(t *testing.T) {
	var result struct {
		Fetch       string `json:"fetch"`
		Process     string `json:"process"`
		EvalBlocked bool   `json:"evalBlocked"`
	}
	source := `export function run() { let blocked = false; try { eval("1+1") } catch (_) { blocked = true; } return { fetch: typeof fetch, process: typeof process, evalBlocked: blocked }; }`
	if err := plugin.RunJSON(t.Context(), source, "run", []byte(`null`), &result); err != nil || result.Fetch != "undefined" || result.Process != "undefined" || !result.EvalBlocked {
		t.Fatalf("capabilities = %+v, error = %v", result, err)
	}
}

func TestRunModuleRejectsImportAndOversizedOutput(t *testing.T) {
	var result any
	if err := plugin.RunJSON(t.Context(), `import x from "node:fs"; export function run() { return x; }`, "run", []byte(`null`), &result); err == nil {
		t.Fatal("accepted an unapproved import")
	}
	if err := plugin.RunJSON(t.Context(), `export function run() { return "x".repeat(2000000); }`, "run", []byte(`null`), &result); err == nil {
		t.Fatal("accepted oversized result")
	}
}

func TestRunModuleHandlesAsyncFulfillmentAndRejection(t *testing.T) {
	var result struct {
		Value int `json:"value"`
	}
	if err := plugin.RunJSON(t.Context(), `export async function run() { await Promise.resolve(); return {value: 42}; }`, "run", []byte(`null`), &result); err != nil || result.Value != 42 {
		t.Fatalf("async result = %+v, %v", result, err)
	}
	if err := plugin.RunJSON(t.Context(), `export async function run() { throw Error("private error"); }`, "run", []byte(`null`), &result); err == nil || strings.Contains(err.Error(), "private error") {
		t.Fatalf("async rejection = %v", err)
	}
	if err := plugin.RunJSON(t.Context(), `export function run() { return new Promise(() => {}); }`, "run", []byte(`null`), &result); err == nil {
		t.Fatal("accepted pending promise")
	}
}

func TestRunModuleInterruptsTopLevelAndIsolatesInstances(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	var ignored any
	if err := plugin.RunJSON(ctx, `for (;;) {} export function run() {}`, "run", []byte(`null`), &ignored); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("top-level interrupt = %v", err)
	}
	const count = 8
	results := make(chan error, count)
	var workers sync.WaitGroup
	for range count {
		workers.Go(func() {
			var result int
			err := plugin.RunJSON(t.Context(), `let counter = 0; export function run() { return ++counter; }`, "run", []byte(`null`), &result)
			if err == nil && result != 1 {
				err = errors.New("plugin global state leaked across calls")
			}
			results <- err
		})
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
}
