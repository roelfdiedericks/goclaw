package delivery

import "testing"

func TestSystemMessageContentForPersistence(t *testing.T) {
	msg := SystemMessage{
		Kind:           SystemKindCron,
		Title:          "Cron: Morning Brief",
		Content:        "Decorated",
		PersistContent: "Raw agent output",
	}

	if got := msg.ContentForPersistence(); got != "Raw agent output" {
		t.Fatalf("expected raw persist content, got %q", got)
	}
}

func TestSystemMessageDisplayText(t *testing.T) {
	msg := SystemMessage{
		Kind:    SystemKindStatus,
		Title:   "Status",
		Content: "Running cron...",
	}

	got := msg.DisplayText()
	want := "**[Status]**\n\nRunning cron..."
	if got != want {
		t.Fatalf("DisplayText mismatch\n got: %q\nwant: %q", got, want)
	}
}
