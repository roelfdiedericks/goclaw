package acp

import "context"

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
