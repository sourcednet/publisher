package publisher

import (
	"bytes"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type extracted struct {
	Title    string
	Language string
	Markdown string
}

// Elements that are never page content.
var alwaysDrop = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true,
	atom.Iframe: true, atom.Form: true, atom.Nav: true, atom.Aside: true,
}

// Elements dropped when falling back to <body>, where they are usually site chrome.
var chromeDrop = map[atom.Atom]bool{atom.Header: true, atom.Footer: true}

// ExtractMarkdown pulls a page's title, language, and main content as
// Markdown, exactly as a build does.
func ExtractMarkdown(src []byte, x Extraction, pageURL string) (title, lang, markdown string, err error) {
	ex, err := extractHTML(src, x, pageURL)
	if err != nil {
		return "", "", "", err
	}
	return ex.Title, ex.Language, ex.Markdown, nil
}

// extractHTML pulls a page's title, language, and main content as Markdown.
// Relative links in the content become absolute against pageURL.
func extractHTML(src []byte, x Extraction, pageURL string) (*extracted, error) {
	drop, err := parseSelectors(x.Drop)
	if err != nil {
		return nil, err
	}
	doc, err := html.Parse(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	ex := &extracted{}
	if n := findFirst(doc, func(n *html.Node) bool { return n.DataAtom == atom.Html }); n != nil {
		ex.Language = attr(n, "lang")
	}
	if n := findFirst(doc, func(n *html.Node) bool { return n.DataAtom == atom.Title }); n != nil {
		ex.Title = collapseSpace(textOf(n))
	}

	content, fromBody := selectContent(doc, x.Selector)
	if content == nil {
		return nil, errors.New("no content element found")
	}
	if ex.Title == "" {
		if h1 := findFirst(content, func(n *html.Node) bool { return n.DataAtom == atom.H1 }); h1 != nil {
			ex.Title = collapseSpace(textOf(h1))
		}
	}
	removeNodes(content, func(n *html.Node) bool {
		if alwaysDrop[n.DataAtom] || (fromBody && chromeDrop[n.DataAtom]) {
			return true
		}
		for _, sel := range drop {
			if sel.matches(n) {
				return true
			}
		}
		return false
	})

	dropSections(content, x.DropSections)
	separateCells(content)

	conv := converter.NewConverter(converter.WithPlugins(
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(),
		table.NewTablePlugin(),
		strikethrough.NewStrikethroughPlugin(),
	))
	md, err := conv.ConvertNode(content, converter.WithDomain(pageURL))
	if err != nil {
		return nil, err
	}
	ex.Markdown = string(md)
	return ex, nil
}

// selectContent finds the main content element. It reports whether it fell
// back to <body>.
func selectContent(doc *html.Node, selector string) (*html.Node, bool) {
	if selector != "" {
		if id, ok := strings.CutPrefix(selector, "#"); ok {
			return findFirst(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && attr(n, "id") == id }), false
		}
		return findFirst(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == selector }), false
	}
	for _, a := range []atom.Atom{atom.Main, atom.Article} {
		if n := findFirst(doc, func(n *html.Node) bool { return n.DataAtom == a }); n != nil {
			return n, false
		}
	}
	return findFirst(doc, func(n *html.Node) bool { return n.DataAtom == atom.Body }), true
}

func findFirst(n *html.Node, match func(*html.Node) bool) *html.Node {
	if match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findFirst(c, match); f != nil {
			return f
		}
	}
	return nil
}

func removeNodes(n *html.Node, drop func(*html.Node) bool) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && drop(c) {
			n.RemoveChild(c)
		} else {
			removeNodes(c, drop)
		}
		c = next
	}
}

// headingLevel returns 1 to 6 for h1 to h6, and 0 for anything else.
func headingLevel(n *html.Node) int {
	if n.Type != html.ElementNode {
		return 0
	}
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		return int(n.Data[1] - '0')
	}
	return 0
}

// sectionHeading returns the heading a node is or wraps, such as
// Wikipedia's <div class="mw-heading"><h2>…</h2></div>, or nil.
func sectionHeading(n *html.Node) *html.Node {
	if headingLevel(n) > 0 {
		return n
	}
	if n.Type != html.ElementNode {
		return nil
	}
	var only *html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.ElementNode && only == nil:
			only = c
		case c.Type == html.ElementNode, c.Type == html.TextNode && strings.TrimSpace(c.Data) != "":
			return nil
		}
	}
	if only != nil && headingLevel(only) > 0 {
		return only
	}
	return nil
}

// dropSections removes the sections whose heading text is one of titles:
// the heading, and its following siblings up to the next heading of the
// same or a higher level.
func dropSections(n *html.Node, titles []string) {
	if len(titles) == 0 {
		return
	}
	for c := n.FirstChild; c != nil; {
		h := sectionHeading(c)
		if h == nil || !slices.ContainsFunc(titles, func(t string) bool { return strings.EqualFold(t, collapseSpace(textOf(h))) }) {
			dropSections(c, titles)
			c = c.NextSibling
			continue
		}
		level := headingLevel(h)
		for c != nil {
			if next := sectionHeading(c); next != nil && next != h && headingLevel(next) <= level {
				break
			}
			gone := c
			c = c.NextSibling
			n.RemoveChild(gone)
		}
	}
}

// separateCells ends every table cell that has another cell after it in
// its row with a space. A table that can't become a Markdown table (an
// infobox holding images or lists, say) is written row by row, and without
// the space a label and its value run together ("Elevation8,848 m").
// Markdown tables trim cells, so they don't change.
func separateCells(n *html.Node) {
	isCell := func(c *html.Node) bool {
		return c != nil && c.Type == html.ElementNode && (c.DataAtom == atom.Th || c.DataAtom == atom.Td)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if isCell(c) {
			for next := c.NextSibling; next != nil; next = next.NextSibling {
				if isCell(next) {
					c.AppendChild(&html.Node{Type: html.TextNode, Data: " "})
					break
				}
			}
		}
		separateCells(c)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// sourcedLink matches the link element that setRecordLink writes.
var sourcedLink = regexp.MustCompile(`[ \t]*<link rel="sourced-record" href="[^"]*">\n?`)

var headClose = regexp.MustCompile(`(?i)</head\s*>`)

// setRecordLink makes the page link to href with
// <link rel="sourced-record">, or removes the link if href is empty. It edits
// the source text directly so the rest of the page stays byte for byte the
// same, and reports whether anything changed.
func setRecordLink(src []byte, href string) ([]byte, bool, error) {
	out := sourcedLink.ReplaceAll(src, nil)
	if href != "" {
		loc := headClose.FindIndex(out)
		if loc == nil {
			return src, false, errors.New("page has no </head> to add the sourced-record link to")
		}
		tag := []byte(`<link rel="sourced-record" href="` + html.EscapeString(href) + "\">\n")
		out = append(out[:loc[0]:loc[0]], append(tag, out[loc[0]:]...)...)
	}
	return out, !bytes.Equal(out, src), nil
}
