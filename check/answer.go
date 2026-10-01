package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sourcednet/core"
)

// answerChunk is a passage as it appears in an answer.
type answerChunk struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Cite string `json:"cite"`
}

// answer holds the parts of any resolver answer that can be checked.
type answer struct {
	VerifiedBy   string          `json:"verified_by"`
	ResolverSig  *core.Signature `json:"resolver_sig"`
	Verification string          `json:"verification"`
	Publisher    string          `json:"publisher"`
	Record       string          `json:"record"`
	Chunks       []answerChunk   `json:"chunks"`
	Chunk        *answerChunk    `json:"chunk"`
	Citation     string          `json:"citation"`
	Current      string          `json:"current"`
	Passage      json.RawMessage `json:"passage"`
	Latest       *answerChunk    `json:"latest"`
	Matches      []struct {
		Chunk string `json:"chunk"`
		Text  string `json:"text"`
	} `json:"matches"`
	Results []struct {
		Publisher string      `json:"publisher"`
		Record    string      `json:"record"`
		Passage   answerChunk `json:"passage"`
	} `json:"results"`
	Originals *struct {
		Records map[string]json.RawMessage `json:"records"`
	} `json:"originals"`
}

// Answer checks a resolver's answer using only public files: the
// resolver's resolver.json for its signature, and each publisher's own
// keys.json for the signed records the answer relies on. Records the answer
// carries as originals are checked as they are; any other is fetched from
// its publisher by ID. Every passage in the answer must hash to its chunk ID
// and belong to the record it cites.
func Answer(ctx context.Context, f Fetcher, raw []byte) *Result {
	res := &Result{Findings: []Finding{}}
	c := &checker{ctx: ctx, f: f, res: res, verified: map[string]*core.Record{}}
	var a answer
	if err := json.Unmarshal(raw, &a); err != nil {
		c.add(SeverityError, "answer", err)
		return res
	}
	res.Publisher = a.VerifiedBy

	switch a.VerifiedBy {
	case "":
		c.add(SeverityError, "answer", errors.New("not a resolver answer: no verified_by"))
		return res
	case "local":
		c.add(SeverityWarning, "answer", errors.New("checked by a local client; there is no resolver signature to check"))
	default:
		c.resolverSignature(a.VerifiedBy, raw, a.ResolverSig)
	}

	if a.Originals != nil {
		for id, rec := range a.Originals.Records {
			c.original(id, rec)
		}
	}
	if a.Verification == "verified" && a.Record != "" {
		r := c.needRecord("answer", a.Publisher, a.Record)
		for _, ch := range a.Chunks {
			c.passage(r, ch)
		}
		if a.Chunk != nil {
			c.passage(r, *a.Chunk)
		}
	}
	if a.Citation != "" && a.Verification == "verified" {
		cite, err := core.ParseCitation(a.Citation)
		if err != nil {
			c.add(SeverityError, "citation", err)
		} else {
			cited := c.needRecord("citation", cite.Publisher, cite.Record)
			var p answerChunk
			if len(a.Passage) > 0 && a.Passage[0] == '{' {
				if err := json.Unmarshal(a.Passage, &p); err == nil {
					if p.ID != cite.Chunk {
						c.add(SeverityError, "passage", fmt.Errorf("answer quotes chunk %s, citation is for %s", p.ID, cite.Chunk))
					}
					c.passage(cited, p)
				}
			}
			if a.Latest != nil && a.Current != "" {
				c.passage(c.needRecord("latest", cite.Publisher, a.Current), *a.Latest)
			}
		}
	}
	for _, r := range a.Results {
		c.passage(c.needRecord("result", r.Publisher, r.Record), r.Passage)
	}
	for _, m := range a.Matches {
		if core.ChunkID(m.Text) != m.Chunk {
			c.add(SeverityError, "match", fmt.Errorf("text of chunk %s does not match its id", m.Chunk))
		}
	}
	res.Records = len(c.verified)
	return res
}

func (c *checker) resolverSignature(name string, raw []byte, sig *core.Signature) {
	if sig == nil {
		c.add(SeverityError, name, errors.New("answer is not signed"))
		return
	}
	infoRaw, _, err := c.f.Fetch(c.ctx, core.ResolverInfoURL(name))
	if err != nil {
		c.add(SeverityError, name, fmt.Errorf("resolver.json: %w", err))
		return
	}
	var info core.ResolverInfo
	if err := json.Unmarshal(infoRaw, &info); err != nil {
		c.add(SeverityError, name, fmt.Errorf("resolver.json: %w", err))
		return
	}
	if !strings.EqualFold(info.Resolver, name) {
		c.add(SeverityError, name, fmt.Errorf("resolver.json names %q", info.Resolver))
		return
	}
	b, err := core.CanonicalBytes(raw, "resolver_sig")
	if err != nil {
		c.add(SeverityError, name, err)
		return
	}
	if err := info.KeySet().VerifySignature(sig, b); err != nil {
		c.add(SeverityError, name, fmt.Errorf("resolver signature: %w", err))
	}
}

// original verifies a record carried in the answer against its publisher's keys.
func (c *checker) original(id string, raw []byte) {
	var head struct {
		Publisher string `json:"publisher"`
	}
	if err := json.Unmarshal(raw, &head); err != nil || head.Publisher == "" {
		c.add(SeverityError, id, errors.New("original record has no publisher"))
		return
	}
	keysRaw, _, err := c.f.Fetch(c.ctx, core.KeysURL(head.Publisher))
	if err != nil {
		c.add(SeverityError, head.Publisher, fmt.Errorf("keys.json: %w", err))
		return
	}
	ks, err := core.ParseKeySet(keysRaw, head.Publisher)
	if err != nil {
		c.add(SeverityError, head.Publisher, err)
		return
	}
	r, err := core.VerifyRecord(raw, ks, head.Publisher)
	if err != nil {
		c.add(SeverityError, id, err)
		return
	}
	if r.ID != id {
		c.add(SeverityError, id, fmt.Errorf("original is record %s", r.ID))
		return
	}
	c.verified[id] = r
}

// needRecord returns a verified record, fetching it from its publisher when
// the answer didn't carry it.
func (c *checker) needRecord(subject, publisher, id string) *core.Record {
	if r, ok := c.verified[id]; ok {
		return r
	}
	u, err := core.RecordURL(publisher, id)
	if err != nil {
		c.add(SeverityError, subject, fmt.Errorf("record %s: %w", id, err))
		return nil
	}
	raw, _, err := c.f.Fetch(c.ctx, u)
	if err != nil {
		c.add(SeverityError, subject, fmt.Errorf("record %s: %w", id, err))
		return nil
	}
	c.original(id, raw)
	return c.verified[id]
}

// passage checks a quoted chunk against the verified record it belongs to.
func (c *checker) passage(r *core.Record, ch answerChunk) {
	if r == nil {
		return
	}
	if err := core.VerifyChunk(r, core.BundleChunk{ID: ch.ID, Text: ch.Text}); err != nil {
		c.add(SeverityError, "passage", err)
		return
	}
	if ch.Cite == "" {
		return
	}
	cite, err := core.ParseCitation(ch.Cite)
	if err != nil || cite.Record != r.ID || cite.Chunk != ch.ID || !strings.EqualFold(cite.Publisher, r.Publisher) {
		c.add(SeverityError, "passage", fmt.Errorf("citation %s does not point to chunk %s of record %s", ch.Cite, ch.ID, r.ID))
	}
}
