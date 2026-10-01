package publisher

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// Extraction says which part of a page is signed content.
type Extraction struct {
	// Selector picks the element holding the main content (see
	// Config.ContentSelector). Empty tries main, article, then body.
	Selector string
	// Drop lists elements inside the content to leave out, as simple
	// selectors (see ParseSelector).
	Drop []string
	// DropSections lists headings whose whole section is left out (see
	// Config.DropSections).
	DropSections []string
}

// selector is a simple CSS selector: a tag, a class, an id, or a tag with a
// class or id ("div", ".navbox", "#toc", "sup.reference").
type selector struct{ tag, class, id string }

// ParseSelector parses a simple selector: tag, .class, #id, tag.class, or tag#id.
func ParseSelector(s string) (selector, error) {
	var sel selector
	rest := strings.TrimSpace(s)
	if i := strings.IndexAny(rest, ".#"); i >= 0 {
		sel.tag, rest = rest[:i], rest[i:]
	} else {
		sel.tag, rest = rest, ""
	}
	switch {
	case strings.HasPrefix(rest, "."):
		sel.class = rest[1:]
	case strings.HasPrefix(rest, "#"):
		sel.id = rest[1:]
	}
	if sel == (selector{}) || strings.ContainsAny(sel.tag+sel.class+sel.id, " .#>+~[]:,") {
		return selector{}, fmt.Errorf("selector %q: want tag, .class, #id, tag.class, or tag#id", s)
	}
	sel.tag = strings.ToLower(sel.tag)
	return sel, nil
}

func (sel selector) matches(n *html.Node) bool {
	if n.Type != html.ElementNode || (sel.tag != "" && n.Data != sel.tag) {
		return false
	}
	if sel.id != "" && attr(n, "id") != sel.id {
		return false
	}
	if sel.class != "" && !hasClass(n, sel.class) {
		return false
	}
	return true
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func parseSelectors(ss []string) ([]selector, error) {
	out := make([]selector, 0, len(ss))
	for _, s := range ss {
		sel, err := ParseSelector(s)
		if err != nil {
			return nil, err
		}
		out = append(out, sel)
	}
	return out, nil
}
