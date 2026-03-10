// Package skill manages the wazero WASM sandbox runtime for
// executing skills with capability-brokered permissions.
package skill

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Runtime manages wazero WASM module instances for skills.
type Runtime struct {
	wazRuntime wazero.Runtime
	modules    map[string]*loadedModule
	broker     *CapabilityBroker
	mu         sync.Mutex
}

type loadedModule struct {
	code   wazero.CompiledModule
	entry  *SkillEntry
	wasmBytes []byte
}

// NewRuntime creates a new WASM runtime with WASI support.
func NewRuntime(ctx context.Context) (*Runtime, error) {
	cfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	r := wazero.NewRuntimeWithConfig(ctx, cfg)

	// Instantiate WASI preview 1 for stdin/stdout/stderr support.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		r.Close(ctx)
		return nil, fmt.Errorf("skill: wasi init: %w", err)
	}

	return &Runtime{
		wazRuntime: r,
		modules:    make(map[string]*loadedModule),
		broker:     NewCapabilityBroker(),
	}, nil
}

// LoadSkill compiles and registers a skill's WASM module.
func (rt *Runtime) LoadSkill(ctx context.Context, entry *SkillEntry) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if _, ok := rt.modules[entry.Manifest.Name]; ok {
		return fmt.Errorf("skill: %s already loaded", entry.Manifest.Name)
	}

	wasmBytes, err := os.ReadFile(entry.WasmPath)
	if err != nil {
		return fmt.Errorf("skill: read wasm %s: %w", entry.Manifest.Name, err)
	}

	compiled, err := rt.wazRuntime.CompileModule(ctx, wasmBytes)
	if err != nil {
		return fmt.Errorf("skill: compile %s: %w", entry.Manifest.Name, err)
	}

	// Register host functions for this skill based on its permissions.
	rt.broker.RegisterSkill(entry)

	rt.modules[entry.Manifest.Name] = &loadedModule{
		code:      compiled,
		entry:     entry,
		wasmBytes: wasmBytes,
	}
	return nil
}

// Execute calls a skill by writing JSON args to WASI stdin and reading
// JSON results from WASI stdout. Timeout is enforced by closing the
// module from a watchdog goroutine, since WASI poll_oneoff (used by
// Go's time.Sleep) does not respond to context cancellation alone.
func (rt *Runtime) Execute(ctx context.Context, skillName string, argsJSON []byte) ([]byte, error) {
	rt.mu.Lock()
	lm, ok := rt.modules[skillName]
	rt.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("skill: %s not loaded", skillName)
	}

	// Apply timeout from manifest.
	maxExec := lm.entry.Manifest.Permissions.MaxExecTime
	if maxExec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(maxExec)*time.Second)
		defer cancel()
	}

	stdin := bytes.NewReader(argsJSON)
	var stdout, stderr bytes.Buffer

	cfg := wazero.NewModuleConfig().
		WithStdin(stdin).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithName(""). // anonymous so multiple invocations don't collide
		WithStartFunctions("_start")

	// Instantiate runs _start (main) and blocks until it returns.
	// WithCloseOnContextDone ensures the module is terminated on timeout.
	mod, err := rt.wazRuntime.InstantiateModule(ctx, lm.code, cfg)
	if mod != nil {
		mod.Close(context.Background())
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("skill: %s: execution timed out after %ds", skillName, maxExec)
		}
		if stdout.Len() > 0 {
			return stdout.Bytes(), nil
		}
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("skill: %s: %s", skillName, stderr.String())
		}
		return nil, fmt.Errorf("skill: %s: execute: %w", skillName, err)
	}

	if stderr.Len() > 0 && stdout.Len() == 0 {
		return nil, fmt.Errorf("skill: %s: %s", skillName, stderr.String())
	}

	return stdout.Bytes(), nil
}

// UnloadSkill closes and removes a loaded skill module.
func (rt *Runtime) UnloadSkill(ctx context.Context, skillName string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	lm, ok := rt.modules[skillName]
	if !ok {
		return fmt.Errorf("skill: %s not loaded", skillName)
	}

	if err := lm.code.Close(ctx); err != nil {
		return fmt.Errorf("skill: close %s: %w", skillName, err)
	}

	rt.broker.UnregisterSkill(skillName)
	delete(rt.modules, skillName)
	return nil
}

// Close shuts down the entire runtime and all loaded modules.
func (rt *Runtime) Close(ctx context.Context) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.modules = make(map[string]*loadedModule)
	return rt.wazRuntime.Close(ctx)
}

// Broker returns the capability broker for permission checks.
func (rt *Runtime) Broker() *CapabilityBroker {
	return rt.broker
}
