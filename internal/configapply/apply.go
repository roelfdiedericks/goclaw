package configapply

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

const (
	// EnvSupervisedChild marks a gateway process launched by the supervisor.
	EnvSupervisedChild = "GOCLAW_SUPERVISED_CHILD"

	// RestartExitCode indicates an intentional supervisor-managed restart.
	RestartExitCode = 75
)

var (
	restartRequested atomic.Bool

	// ErrRestartRequested is returned by the gateway when it should exit and let
	// the supervisor restart it.
	ErrRestartRequested = errors.New("configapply: restart requested")
)

type Caller string

const (
	CallerWebIntegrated Caller = "web_integrated"
	CallerWebStandalone Caller = "web_standalone"
)

type RuntimeMode string

const (
	RuntimeModeNone            RuntimeMode = "none"
	RuntimeModeForegroundGateway RuntimeMode = "foreground_gateway"
	RuntimeModeSupervisedChild RuntimeMode = "supervised_child"
)

type RestartCapability string

const (
	RestartCapabilityNone          RestartCapability = "none"
	RestartCapabilityInstructionOnly RestartCapability = "instruction_only"
	RestartCapabilityAuto          RestartCapability = "auto"
)

type Action string

const (
	ActionNone              Action = "none"
	ActionManualRestart     Action = "manual_restart"
	ActionSupervisedRestart Action = "supervised_restart"
)

type Result struct {
	RuntimeMode       RuntimeMode       `json:"runtimeMode"`
	RestartRequired   bool              `json:"restartRequired"`
	RestartCapability RestartCapability `json:"restartCapability"`
	Action            Action            `json:"action"`
	WaitForRestart    bool              `json:"waitForRestart,omitempty"`
	Message           string            `json:"message"`
}

func DetectRuntimeMode(caller Caller) RuntimeMode {
	if caller == CallerWebStandalone {
		return RuntimeModeNone
	}
	if os.Getenv(EnvSupervisedChild) == "1" {
		return RuntimeModeSupervisedChild
	}
	return RuntimeModeForegroundGateway
}

func Decide(caller Caller) Result {
	mode := DetectRuntimeMode(caller)
	switch mode {
	case RuntimeModeSupervisedChild:
		return Result{
			RuntimeMode:       mode,
			RestartRequired:   true,
			RestartCapability: RestartCapabilityAuto,
			Action:            ActionSupervisedRestart,
			WaitForRestart:    true,
			Message:           "Configuration saved. Waiting for GoClaw to restart...",
		}
	case RuntimeModeForegroundGateway:
		return Result{
			RuntimeMode:       mode,
			RestartRequired:   true,
			RestartCapability: RestartCapabilityInstructionOnly,
			Action:            ActionManualRestart,
			Message:           "Configuration saved. Restart the gateway to apply changes.",
		}
	default:
		return Result{
			RuntimeMode:       mode,
			RestartRequired:   false,
			RestartCapability: RestartCapabilityNone,
			Action:            ActionNone,
			Message:           "Configuration saved.",
		}
	}
}

func ScheduleSupervisorRestart(delay time.Duration) error {
	if os.Getenv(EnvSupervisedChild) != "1" {
		return fmt.Errorf("configapply: process is not a supervised gateway child")
	}
	if delay < 0 {
		delay = 0
	}
	restartRequested.Store(true)

	go func() {
		time.Sleep(delay)
		L_info("configapply: exiting for supervisor restart", "delay", delay)
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			L_error("configapply: failed to signal self for restart", "error", err)
		}
	}()
	return nil
}

func RestartRequested() bool {
	return restartRequested.Load()
}

func IsRestartRequestedError(err error) bool {
	return errors.Is(err, ErrRestartRequested)
}
