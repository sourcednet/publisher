# publisher

`sourced-publisher` signs a site for sourced.net: it turns each page's main content into Markdown chunks, signs them, and writes the signed files under `/.well-known/sourced/` in the web root, where any web server serves them. It also checks a project, a web root, a live domain, or a resolver's signed answer, using only public files.

Spec: `../../Docs/sourced.net — Core Spec v1.md`. Go 1.24 or later.

```
make            # lint, test, build: bin/sourced-publisher
```

## Signing a site

```
sourced-publisher init -publisher example.org my-site   # sourced.json, a first key, keys.json
# put the site's built HTML in my-site/public
sourced-publisher build my-site                         # sign pages, write /.well-known/sourced/, add link tags
sourced-publisher check my-site                         # verify everything, as a verifier would
```

Later changes:

```
sourced-publisher build my-site                                                    # changed pages become revisions
sourced-publisher build -page guides/a.html -correction "Fixed the date." my-site  # declare a correction
sourced-publisher build -page guides/b.html -withdraw my-site                      # withdraw: record it, delete the text
sourced-publisher keygen my-site                                                   # rotate the signing key
sourced-publisher revoke 2026a my-site                                             # revoke a key and re-sign what it signed
sourced-publisher check example.org                                                # check a live domain over HTTPS
sourced-publisher check answer.json                                                # check a resolver's signed answer
```

A project keeps its private keys in `.sourced/keys/`, outside the web root. Deploy only the web root.

## What gets signed

`sourced.json` says what of each page is content:

- **`content_selector`**: the element holding a page's main content (a tag, `#id`, …). Empty tries `main`, `article`, then `body` without site chrome.
- **`drop_selectors`**: elements inside the content to leave out, such as reference lists or navigation boxes (tag, `.class`, `#id`, `tag.class`, `tag#id`).
- **`drop_sections`**: section headings whose whole section is left out, such as "See also" or "Related articles".

Sign the page's own content, not site chrome or link lists: it costs AI apps tokens and pushes real content down in ranking. Leave out automatic numbering such as footnote markers, which renumbers and changes every later chunk when one is added.

## Packages

- `publisher` (this directory): projects, keys, extraction (HTML to Markdown), building and signing.
- `check`: checks sites and signed answers from public files.
