package telegram

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// TelegramRenderer renders markdown to Telegram-compatible HTML
type TelegramRenderer struct {
	html.Config
}

// NewTelegramRenderer creates a new Telegram HTML renderer
func NewTelegramRenderer() renderer.Renderer {
	r := &TelegramRenderer{
		Config: html.NewConfig(),
	}
	return renderer.NewRenderer(
		renderer.WithNodeRenderers(
			util.Prioritized(r, 100),
		),
	)
}

// RegisterFuncs registers rendering functions for node types
func (r *TelegramRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	// Block elements
	reg.Register(ast.KindDocument, r.renderDocument)
	reg.Register(ast.KindParagraph, r.renderParagraph)
	reg.Register(ast.KindHeading, r.renderHeading)
	reg.Register(ast.KindCodeBlock, r.renderCodeBlock)
	reg.Register(ast.KindFencedCodeBlock, r.renderCodeBlock)
	reg.Register(ast.KindBlockquote, r.renderBlockquote)
	reg.Register(ast.KindList, r.renderList)
	reg.Register(ast.KindListItem, r.renderListItem)
	reg.Register(ast.KindThematicBreak, r.renderThematicBreak)

	// Inline elements
	reg.Register(ast.KindText, r.renderText)
	reg.Register(ast.KindString, r.renderString)
	reg.Register(ast.KindEmphasis, r.renderEmphasis)
	reg.Register(ast.KindCodeSpan, r.renderCodeSpan)
	reg.Register(ast.KindLink, r.renderLink)
	reg.Register(ast.KindAutoLink, r.renderAutoLink)
	reg.Register(ast.KindRawHTML, r.renderRawHTML)

	// GFM Table - render as preformatted text
	reg.Register(east.KindTable, r.renderTable)
	reg.Register(east.KindTableHeader, r.renderTableHeader)
	reg.Register(east.KindTableRow, r.renderTableRow)
	reg.Register(east.KindTableCell, r.renderTableCell)

	// GFM extras
	reg.Register(east.KindStrikethrough, r.renderStrikethrough)
}

func (r *TelegramRenderer) renderDocument(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderParagraph(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		w.WriteString("\n\n")
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderHeading(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		w.WriteString("<b>")
	} else {
		w.WriteString("</b>\n\n")
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		w.WriteString("<pre>")
		lines := node.Lines()
		for i := 0; i < lines.Len(); i++ {
			line := lines.At(i)
			w.WriteString(escapeHTMLString(string(line.Value(source))))
		}
		w.WriteString("</pre>\n")
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderThematicBreak(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		w.WriteString("\n---\n")
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderBlockquote(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		w.WriteString("<blockquote>")
	} else {
		w.WriteString("</blockquote>\n")
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderList(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		w.WriteString("\n")
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderListItem(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		w.WriteString("• ")
	} else {
		w.WriteString("\n")
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderText(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*ast.Text) //nolint:errcheck // renderer registered for this node type
		w.WriteString(escapeHTMLString(string(n.Segment.Value(source))))
		if n.SoftLineBreak() {
			w.WriteString("\n")
		}
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderString(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*ast.String) //nolint:errcheck // renderer registered for this node type
		w.WriteString(escapeHTMLString(string(n.Value)))
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderEmphasis(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Emphasis) //nolint:errcheck // renderer registered for this node type
	if n.Level == 2 {
		// Bold
		if entering {
			w.WriteString("<b>")
		} else {
			w.WriteString("</b>")
		}
	} else {
		// Italic
		if entering {
			w.WriteString("<i>")
		} else {
			w.WriteString("</i>")
		}
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderCodeSpan(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		w.WriteString("<code>")
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				w.WriteString(escapeHTMLString(string(t.Segment.Value(source))))
			}
		}
		w.WriteString("</code>")
		return ast.WalkSkipChildren, nil
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Link) //nolint:errcheck // renderer registered for this node type
	if entering {
		w.WriteString(`<a href="`)
		w.WriteString(escapeHTMLString(string(n.Destination)))
		w.WriteString(`">`)
	} else {
		w.WriteString("</a>")
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderAutoLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.AutoLink) //nolint:errcheck // renderer registered for this node type
	if entering {
		url := n.URL(source)
		w.WriteString(`<a href="`)
		w.WriteString(escapeHTMLString(string(url)))
		w.WriteString(`">`)
		w.WriteString(escapeHTMLString(string(url)))
		w.WriteString("</a>")
		return ast.WalkSkipChildren, nil
	}
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderRawHTML(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	// Skip raw HTML - don't render it
	return ast.WalkSkipChildren, nil
}

func (r *TelegramRenderer) renderStrikethrough(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		w.WriteString("<s>")
	} else {
		w.WriteString("</s>")
	}
	return ast.WalkContinue, nil
}

// Table rendering - output as preformatted text since Telegram doesn't support HTML tables
func (r *TelegramRenderer) renderTable(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		tableText, links := r.renderTableAsText(source, node)
		w.WriteString("<pre>")
		w.WriteString(escapeHTMLString(tableText))
		w.WriteString("</pre>\n")
		r.renderTableLinks(w, links)
		return ast.WalkSkipChildren, nil
	}
	return ast.WalkContinue, nil
}

type tableLinkRef struct {
	Index int
	URL   string
}

type tableLinkRegistry struct {
	order []tableLinkRef
	ids   map[string]int
}

func newTableLinkRegistry() *tableLinkRegistry {
	return &tableLinkRegistry{
		order: make([]tableLinkRef, 0, 8),
		ids:   make(map[string]int),
	}
}

func (r *tableLinkRegistry) indexForURL(url string) int {
	u := strings.TrimSpace(url)
	if u == "" {
		return 0
	}
	if idx, ok := r.ids[u]; ok {
		return idx
	}
	idx := len(r.order) + 1
	r.ids[u] = idx
	r.order = append(r.order, tableLinkRef{Index: idx, URL: u})
	return idx
}

func (r *TelegramRenderer) renderTableAsText(source []byte, table ast.Node) (string, []tableLinkRef) {
	links := newTableLinkRegistry()
	rows := make([][]string, 0, 8)

	// First pass: calculate column widths (rune count - emojis may not align perfectly)
	var colWidths []int
	for row := table.FirstChild(); row != nil; row = row.NextSibling() {
		rowTexts := make([]string, 0, 6)
		col := 0
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cellText := r.getCellText(source, cell, links)
			width := utf8.RuneCountInString(cellText)
			if col >= len(colWidths) {
				colWidths = append(colWidths, width)
			} else if width > colWidths[col] {
				colWidths[col] = width
			}
			rowTexts = append(rowTexts, cellText)
			col++
		}
		rows = append(rows, rowTexts)
	}

	// Second pass: render with padding
	var out bytes.Buffer
	isHeader := true
	for _, row := range rows {
		out.WriteString("|")
		for col, cellText := range row {
			out.WriteString(" ")
			out.WriteString(cellText)
			// Pad to column width
			if col < len(colWidths) {
				padding := colWidths[col] - utf8.RuneCountInString(cellText)
				if padding > 0 {
					out.WriteString(strings.Repeat(" ", padding))
				}
			}
			out.WriteString(" |")
		}
		out.WriteString("\n")

		// Add separator after header
		if isHeader {
			out.WriteString("|")
			for _, width := range colWidths {
				out.WriteString("-")
				out.WriteString(strings.Repeat("-", width))
				out.WriteString("-|")
			}
			out.WriteString("\n")
			isHeader = false
		}
	}
	return out.String(), links.order
}

func (r *TelegramRenderer) renderTableLinks(w util.BufWriter, links []tableLinkRef) {
	if len(links) == 0 {
		return
	}
	w.WriteString("<b>Links:</b>\n")
	for _, ref := range links {
		w.WriteString(fmt.Sprintf("[%d] ", ref.Index))
		w.WriteString(`<a href="`)
		w.WriteString(escapeHTMLString(ref.URL))
		w.WriteString(`">`)
		w.WriteString(escapeHTMLString(ref.URL))
		w.WriteString("</a>\n")
	}
	w.WriteString("\n")
}

func (r *TelegramRenderer) getCellText(source []byte, cell ast.Node, links *tableLinkRegistry) string {
	var buf bytes.Buffer
	for child := cell.FirstChild(); child != nil; child = child.NextSibling() {
		r.extractTableText(&buf, source, child, links)
	}
	return strings.TrimSpace(buf.String())
}

func (r *TelegramRenderer) extractTableText(buf *bytes.Buffer, source []byte, node ast.Node, links *tableLinkRegistry) {
	switch n := node.(type) {
	case *ast.Text:
		buf.Write(n.Segment.Value(source))
	case *ast.String:
		buf.Write(n.Value)
	case *ast.Link:
		r.extractLinkLabel(buf, source, node, links.indexForURL(string(n.Destination)))
	case *ast.AutoLink:
		url := strings.TrimSpace(string(n.URL(source)))
		if url == "" {
			return
		}
		ref := links.indexForURL(url)
		if ref <= 0 {
			buf.WriteString(url)
			return
		}
		buf.WriteString(url)
		buf.WriteString(fmt.Sprintf(" [%d]", ref))
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			r.extractTableText(buf, source, child, links)
		}
	}
}

func (r *TelegramRenderer) extractLinkLabel(buf *bytes.Buffer, source []byte, node ast.Node, ref int) {
	var label bytes.Buffer
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		r.extractText(&label, source, child)
	}
	text := strings.TrimSpace(label.String())
	if text != "" {
		buf.WriteString(text)
	}
	if ref > 0 {
		if text != "" {
			buf.WriteString(" ")
		}
		buf.WriteString(fmt.Sprintf("[%d]", ref))
	}
}

func (r *TelegramRenderer) extractText(buf *bytes.Buffer, source []byte, node ast.Node) {
	switch n := node.(type) {
	case *ast.Text:
		buf.Write(n.Segment.Value(source))
	case *ast.String:
		buf.Write(n.Value)
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			r.extractText(buf, source, child)
		}
	}
}

func (r *TelegramRenderer) renderTableHeader(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	// Handled by renderTable
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderTableRow(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	// Handled by renderTable
	return ast.WalkContinue, nil
}

func (r *TelegramRenderer) renderTableCell(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	// Handled by renderTable
	return ast.WalkContinue, nil
}

// escapeHTMLString escapes HTML special characters in a string
func escapeHTMLString(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}

// FormatMessage converts markdown to Telegram-compatible HTML.
// If conversion fails, returns the original markdown as fallback.
func FormatMessage(markdown string) string {
	if markdown == "" {
		return ""
	}

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRenderer(NewTelegramRenderer()),
	)

	var buf bytes.Buffer
	if err := md.Convert([]byte(markdown), &buf); err != nil {
		// Fallback to raw text on conversion error
		return markdown
	}

	result := strings.TrimSpace(buf.String())
	if result == "" {
		return markdown
	}

	return result
}

// FormatMessageSafe converts markdown to Telegram HTML, returning both
// the formatted result and whether conversion succeeded.
// Use this when you need to know if fallback was used.
func FormatMessageSafe(markdown string) (formatted string, ok bool) {
	if markdown == "" {
		return "", true
	}

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRenderer(NewTelegramRenderer()),
	)

	var buf bytes.Buffer
	if err := md.Convert([]byte(markdown), &buf); err != nil {
		return markdown, false
	}

	result := strings.TrimSpace(buf.String())
	if result == "" {
		return markdown, false
	}

	return result, true
}
