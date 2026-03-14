package configapply

import "testing"

func TestDecide(t *testing.T) {
	t.Setenv(EnvSupervisedChild, "")

	standalone := Decide(CallerWebStandalone)
	if standalone.RuntimeMode != RuntimeModeNone || standalone.RestartRequired {
		t.Fatalf("standalone apply result unexpected: %+v", standalone)
	}

	foreground := Decide(CallerWebIntegrated)
	if foreground.RuntimeMode != RuntimeModeForegroundGateway {
		t.Fatalf("foreground mode = %q", foreground.RuntimeMode)
	}
	if foreground.Action != ActionManualRestart || foreground.RestartCapability != RestartCapabilityInstructionOnly {
		t.Fatalf("foreground apply result unexpected: %+v", foreground)
	}

	t.Setenv(EnvSupervisedChild, "1")
	supervised := Decide(CallerWebIntegrated)
	if supervised.RuntimeMode != RuntimeModeSupervisedChild {
		t.Fatalf("supervised mode = %q", supervised.RuntimeMode)
	}
	if supervised.Action != ActionSupervisedRestart || supervised.RestartCapability != RestartCapabilityAuto || !supervised.WaitForRestart {
		t.Fatalf("supervised apply result unexpected: %+v", supervised)
	}
}
