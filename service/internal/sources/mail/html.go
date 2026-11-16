package mail

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var markdown = goldmark.New(goldmark.WithExtensions(
	extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute)),
	extension.Strikethrough, extension.Linkify, extension.TaskList,
))

// Styles go inline on each tag, since most mail apps drop style sheets.
var inline = strings.NewReplacer(
	"<table>", `<table style="border-collapse:collapse;margin:8px 0">`,
	"<th>", `<th style="border:1px solid #ddd;padding:4px 10px;text-align:left">`,
	"<th ", `<th style="border:1px solid #ddd;padding:4px 10px" `,
	"<td>", `<td style="border:1px solid #ddd;padding:4px 10px">`,
	"<td ", `<td style="border:1px solid #ddd;padding:4px 10px" `,
	"<pre>", `<pre style="background:#f5f4fb;border-radius:8px;padding:10px;overflow-x:auto;font-size:13px">`,
	"<code>", `<code style="font-family:Menlo,Consolas,monospace;background:#f5f4fb;padding:1px 4px;border-radius:4px">`,
	"<blockquote>", `<blockquote style="margin:8px 0;padding:2px 12px;border-left:4px solid #ddd;color:#555">`,
)

// HTML renders a reply's markdown as an email body. Raw HTML in the text is
// dropped, never passed through, since a reply can carry text from web pages.
func HTML(text string) string {
	var b bytes.Buffer
	if err := markdown.Convert([]byte(text), &b); err != nil {
		b.Reset()
		b.WriteString("<pre>" + escape(text) + "</pre>")
	}
	return `<!doctype html><html><body><div style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:15px;line-height:1.45;color:#222">` +
		inline.Replace(b.String()) + "</div></body></html>"
}

var escape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace
