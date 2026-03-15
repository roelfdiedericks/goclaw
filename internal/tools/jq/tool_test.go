package jq

import "testing"

func TestResolveSandboxModes(t *testing.T) {
	fileSandboxed, execSandboxed := resolveSandboxModes(true, false, true)
	if !fileSandboxed {
		t.Fatal("expected file-mode sandboxing to remain enabled")
	}
	if execSandboxed {
		t.Fatal("expected exec-mode sandboxing to stay disabled when exec sandbox is off")
	}

	fileSandboxed, execSandboxed = resolveSandboxModes(true, true, false)
	if fileSandboxed || execSandboxed {
		t.Fatal("expected per-user sandbox disable to override both jq modes")
	}
}
