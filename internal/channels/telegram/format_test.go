package telegram

import (
	"strings"
	"testing"
)

func TestFormatMessage_TableLinksAreFootnoted(t *testing.T) {
	input := `
| Name | Browse |
|---|---|
| Brave | [Open](https://search.brave.com) |
| Perplexity | [Open](https://www.perplexity.ai) |
`

	out := FormatMessage(input)

	if !strings.Contains(out, "<pre>") {
		t.Fatalf("expected table rendered in <pre>, got: %s", out)
	}
	if !strings.Contains(out, "Open [1]") || !strings.Contains(out, "Open [2]") {
		t.Fatalf("expected numbered link refs in table cells, got: %s", out)
	}
	if !strings.Contains(out, "<b>Links:</b>") {
		t.Fatalf("expected links section after table, got: %s", out)
	}
	if !strings.Contains(out, `[1] <a href="https://search.brave.com">https://search.brave.com</a>`) {
		t.Fatalf("expected clickable link for first ref, got: %s", out)
	}
	if !strings.Contains(out, `[2] <a href="https://www.perplexity.ai">https://www.perplexity.ai</a>`) {
		t.Fatalf("expected clickable link for second ref, got: %s", out)
	}
}

func TestFormatMessage_TableLinksDeduplicateByURL(t *testing.T) {
	input := `
| Provider | Web | Docs |
|---|---|---|
| Brave | [Site](https://search.brave.com) | [Docs](https://search.brave.com) |
`

	out := FormatMessage(input)

	if strings.Count(out, "<b>Links:</b>") != 1 {
		t.Fatalf("expected single links section, got: %s", out)
	}
	if !strings.Contains(out, "Site [1]") || !strings.Contains(out, "Docs [1]") {
		t.Fatalf("expected both cell links to reuse same reference id, got: %s", out)
	}
	if strings.Count(out, `href="https://search.brave.com"`) != 1 {
		t.Fatalf("expected one deduplicated footnote link, got: %s", out)
	}
}
