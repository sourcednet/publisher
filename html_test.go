package publisher

import (
	"strings"
	"testing"
)

const samplePage = `<!doctype html>
<html lang="en-GB">
<head>
<meta charset="utf-8">
<title>Caring for old books</title>
</head>
<body>
<header><a href="/">Example Library home</a></header>
<nav><a href="/guides/">All guides</a></nav>
<main>
<h1>Caring for old books</h1>
<p>Old books last for centuries when they are stored well.</p>
<nav>On this page: humidity</nav>
<h2>Humidity</h2>
<p>Keep humidity steady. See <a href="/guides/digitization.html">digitizing</a>.</p>
<script>track()</script>
</main>
<footer>Copyright Example Library</footer>
</body>
</html>
`

func TestExtractHTML(t *testing.T) {
	ex, err := extractHTML([]byte(samplePage), Extraction{}, "https://example-library.test/guides/book-care.html")
	if err != nil {
		t.Fatal(err)
	}
	if ex.Title != "Caring for old books" || ex.Language != "en-GB" {
		t.Fatalf("title %q, language %q", ex.Title, ex.Language)
	}
	for _, want := range []string{"# Caring for old books", "## Humidity", "stored well", "(https://example-library.test/guides/digitization.html)"} {
		if !strings.Contains(ex.Markdown, want) {
			t.Errorf("markdown lacks %q:\n%s", want, ex.Markdown)
		}
	}
	for _, unwanted := range []string{"home", "All guides", "On this page", "track()", "Copyright"} {
		if strings.Contains(ex.Markdown, unwanted) {
			t.Errorf("markdown contains %q:\n%s", unwanted, ex.Markdown)
		}
	}
}

func TestExtractHTMLBodyFallbackDropsChrome(t *testing.T) {
	src := `<html><head><title>T</title></head><body><header>Site header</header><p>The content.</p><footer>Site footer</footer></body></html>`
	ex, err := extractHTML([]byte(src), Extraction{}, "https://x.test/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ex.Markdown, "The content.") || strings.Contains(ex.Markdown, "Site header") || strings.Contains(ex.Markdown, "Site footer") {
		t.Fatalf("unexpected markdown: %q", ex.Markdown)
	}
}

func TestExtractHTMLSelector(t *testing.T) {
	src := `<html><body><div id="post"><p>Wanted.</p></div><div><p>Unwanted.</p></div></body></html>`
	ex, err := extractHTML([]byte(src), Extraction{Selector: "#post"}, "https://x.test/")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(ex.Markdown) != "Wanted." {
		t.Fatalf("got %q", ex.Markdown)
	}
	if _, err := extractHTML([]byte(src), Extraction{Selector: "#missing"}, "https://x.test/"); err == nil {
		t.Fatal("missing selector should fail")
	}
}

func TestSetRecordLink(t *testing.T) {
	src := []byte("<html>\n<head>\n<title>T</title>\n</head>\n<body>x</body>\n</html>\n")
	out, changed, err := setRecordLink(src, "/.well-known/sourced/records/aa.json")
	if err != nil || !changed {
		t.Fatalf("insert: changed=%v err=%v", changed, err)
	}
	want := "<html>\n<head>\n<title>T</title>\n<link rel=\"sourced-record\" href=\"/.well-known/sourced/records/aa.json\">\n</head>\n<body>x</body>\n</html>\n"
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	if _, changed, _ := setRecordLink(out, "/.well-known/sourced/records/aa.json"); changed {
		t.Fatal("setting the same link again should change nothing")
	}
	replaced, _, _ := setRecordLink(out, "/.well-known/sourced/records/bb.json")
	if strings.Count(string(replaced), "sourced-record") != 1 || !strings.Contains(string(replaced), "bb.json") {
		t.Fatalf("replace failed:\n%s", replaced)
	}
	removed, changed, _ := setRecordLink(replaced, "")
	if !changed || string(removed) != string(src) {
		t.Fatalf("remove should restore the original page:\n%s", removed)
	}
	if _, _, err := setRecordLink([]byte("<p>no head</p>"), "/x"); err == nil {
		t.Fatal("page without </head> should fail")
	}
}

func TestPageURL(t *testing.T) {
	c := DefaultConfig("example.test", "public")
	clean := DefaultConfig("example.test", "public")
	clean.CleanURLs = true
	for _, tt := range []struct {
		c    *Config
		rel  string
		want string
	}{
		{c, "index.html", "https://example.test/"},
		{c, "guides/index.html", "https://example.test/guides/"},
		{c, "guides/book-care.html", "https://example.test/guides/book-care.html"},
		{clean, "guides/book-care.html", "https://example.test/guides/book-care"},
		{c, "notes/first post.md", "https://example.test/notes/first%20post"},
		{c, "index.md", "https://example.test/"},
	} {
		if got := pageURL(tt.c, tt.rel); got != tt.want {
			t.Errorf("pageURL(%q) = %q, want %q", tt.rel, got, tt.want)
		}
	}
}

func TestExtractHTMLDropSelectors(t *testing.T) {
	src := `<html><body><main><p>Kept prose.<sup class="reference">[1]</sup></p>
<div class="hatnote">Main article: Elsewhere</div><figure><figcaption>A mascot</figcaption></figure>
<ol class="references"><li>A citation</li></ol><table class="infobox"><tr><td>Launched 1969</td></tr></table></main></body></html>`
	ex, err := extractHTML([]byte(src), Extraction{Drop: []string{"sup.reference", ".hatnote", "figure", "ol.references"}}, "https://x.test/")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"[1]", "Main article", "mascot", "A citation"} {
		if strings.Contains(ex.Markdown, gone) {
			t.Errorf("kept %q:\n%s", gone, ex.Markdown)
		}
	}
	for _, kept := range []string{"Kept prose.", "Launched 1969"} {
		if !strings.Contains(ex.Markdown, kept) {
			t.Errorf("lost %q:\n%s", kept, ex.Markdown)
		}
	}
	if _, err := extractHTML([]byte(src), Extraction{Drop: []string{"div > p"}}, "https://x.test/"); err == nil {
		t.Fatal("complex selectors should be rejected")
	}
}

func TestExtractHTMLDropSections(t *testing.T) {
	src := `<html><body><main><h1>Linux</h1><p>Prose stays.</p>
<div class="mw-heading mw-heading2"><h2 id="See_also">See also</h2></div><ul><li>Criticism of Linux</li></ul>
<h3>Lists</h3><p>Still in See also.</p>
<div class="mw-heading mw-heading2"><h2>Legacy</h2></div><p>Kept after it.</p>
<h2>External links</h2><p>Official site</p></main></body></html>`
	ex, err := extractHTML([]byte(src), Extraction{DropSections: []string{"see also", "External links"}}, "https://x.test/")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"See also", "Criticism", "Still in", "External links", "Official site"} {
		if strings.Contains(ex.Markdown, gone) {
			t.Errorf("kept %q:\n%s", gone, ex.Markdown)
		}
	}
	for _, kept := range []string{"Prose stays.", "## Legacy", "Kept after it."} {
		if !strings.Contains(ex.Markdown, kept) {
			t.Errorf("lost %q:\n%s", kept, ex.Markdown)
		}
	}
}

func TestExtractHTMLSeparatesTableCells(t *testing.T) {
	// The image makes this infobox impossible to write as a Markdown table,
	// so it is written row by row.
	infobox := `<html><body><main><table class="infobox"><tr><td colspan="2"><img src="a.jpg"><br>Caption</td></tr>
<tr><th><a href="/wiki/Summit">Elevation</a></th><td>8,848 m</td></tr><tr><th>Landing date</th><td>July 20, 1969</td></tr></table>
<table><tr><th>Key</th><th>Value</th></tr><tr><td>a</td><td>1</td></tr></table></main></body></html>`
	ex, err := extractHTML([]byte(infobox), Extraction{}, "https://x.test/")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Elevation](https://x.test/wiki/Summit) 8,848 m", "Landing date July 20, 1969", "| Key | Value |", "| a   | 1     |"} {
		if !strings.Contains(ex.Markdown, want) {
			t.Errorf("lacks %q:\n%s", want, ex.Markdown)
		}
	}
}

func TestExtractHTMLFlattensCodeLines(t *testing.T) {
	// Expressive Code (Starlight's highlighter) writes each line as a div.
	src := `<html><body><main><p>Example:</p><pre data-language="kriol"><code><div class="ec-line"><div class="code"><span>fn soma(nter a) nter {</span></div></div><div class="ec-line"><div class="code"><span class="indent">    </span><span>divolvi a;</span></div></div><div class="ec-line"><div class="code"><span>}</span></div></div></code></pre></main></body></html>`
	ex, err := extractHTML([]byte(src), Extraction{}, "https://x.test/")
	if err != nil {
		t.Fatal(err)
	}
	want := "```kriol\nfn soma(nter a) nter {\n    divolvi a;\n}\n```"
	if !strings.Contains(ex.Markdown, want) {
		t.Fatalf("want %q in:\n%s", want, ex.Markdown)
	}
	plain := `<html><body><main><pre><code>a := 1
b := 2</code></pre></main></body></html>`
	if ex, _ := extractHTML([]byte(plain), Extraction{}, "https://x.test/"); !strings.Contains(ex.Markdown, "a := 1\nb := 2") {
		t.Fatalf("plain code block changed:\n%s", ex.Markdown)
	}
}
