package acp

import (
	"context"
	"fmt"
	"strings"
)

const (
	TransportLocalStdio = "local-stdio"
	DriverCursor        = "cursor"
)

type CursorDriver struct{}

func NewCursorDriver() *CursorDriver { return &CursorDriver{} }

func (d *CursorDriver) ID() string { return DriverCursor }

func (d *CursorDriver) SupportsTransport(transportID string) bool {
	return transportID == TransportLocalStdio
}

func (d *CursorDriver) LaunchSpec(ctx context.Context, req LaunchSpecRequest) (LaunchSpec, error) {
	return LaunchSpec{
		Command: "agent",
		Args:    []string{"acp"},
		Env:     req.Env,
	}, nil
}

func (d *CursorDriver) AuthMethodID() string {
	return "cursor_login"
}

func (d *CursorDriver) KnownModelCatalog() *ACPModelState {
	return knownModelCatalog(d.ID())
}

func (d *CursorDriver) CachedModelCatalog() *ACPModelState {
	return cachedModelCatalog(d.ID())
}

func (d *CursorDriver) EffectiveModelCatalog() *ACPModelState {
	if cached := d.CachedModelCatalog(); cached != nil && len(cached.Options) > 0 {
		return cached
	}
	return d.KnownModelCatalog()
}

func (d *CursorDriver) RefreshModelCatalog(ctx context.Context, req ModelCatalogRefreshRequest) (*ACPModelState, error) {
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		return nil, fmt.Errorf("cwd is required to refresh ACP models")
	}
	transport := NewLocalStdioTransport()
	stdio, ok := transport.(*localStdioTransport)
	if !ok {
		return nil, fmt.Errorf("local stdio ACP transport is unavailable")
	}
	runtime, err := stdio.spawnRuntime(ctx, d, cwd, "")
	if err != nil {
		return nil, fmt.Errorf("start cursor ACP runtime: %w", err)
	}
	defer func() {
		_ = stdio.closeRuntime(context.Background(), runtime)
	}()
	resp, err := stdio.newSessionRaw(ctx, runtime, cwd)
	if err != nil {
		return nil, fmt.Errorf("query cursor ACP model catalog: %w", err)
	}
	state := extractModelState(resp.ConfigOptions)
	if state == nil || len(state.Options) == 0 {
		return nil, fmt.Errorf("cursor ACP did not return any model options")
	}
	setCachedModelCatalog(d.ID(), state)
	return cloneModelState(state), nil
}
