package commands

import "testing"

func TestMatchPhraseSingleWordRepeated(t *testing.T) {
	t.Parallel()

	if !matchPhrase("stop stop", []string{"STOP"}) {
		t.Fatalf("expected repeated single-word phrase to match")
	}
	if matchPhrase("stop now", []string{"STOP"}) {
		t.Fatalf("expected mixed words not to match single-word phrase")
	}
}

func TestMatchPhraseMultiWordExact(t *testing.T) {
	t.Parallel()

	if !matchPhrase("shutdown now", []string{"SHUTDOWN NOW"}) {
		t.Fatalf("expected exact multi-word phrase to match")
	}
	if matchPhrase("please shutdown now", []string{"SHUTDOWN NOW"}) {
		t.Fatalf("expected non-exact multi-word phrase not to match")
	}
}
