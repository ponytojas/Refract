// Package openalex searches published works on OpenAlex and gets their BibTeX
// from doi.org (the DOI's registration agency), so entries come from the
// publisher's metadata and never from a model.
package openalex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/ponytojas/Refract/internal/refs"
)

// Work is one published work.
type Work struct {
	ID       string   `json:"id"` // W3174556939
	DOI      string   `json:"doi"`
	Title    string   `json:"title"`
	Authors  []string `json:"authors"`
	Year     int      `json:"year"`
	Venue    string   `json:"venue"`
	Abstract string   `json:"abstract"`
	CitedBy  int      `json:"cited_by"`
}

// Client calls api.openalex.org. Without APIKey requests share a small free
// daily budget per IP address; a free key raises it.
type Client struct {
	HTTP    *http.Client
	BaseURL string // https://api.openalex.org
	DOIURL  string // https://doi.org
	APIKey  string
}

const fields = "id,doi,display_name,publication_year,authorships,primary_location,abstract_inverted_index,cited_by_count"

// Search returns works ranked by OpenAlex's relevance.
func (c *Client) Search(ctx context.Context, query string, n int) ([]Work, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("empty query")
	}
	var body struct {
		Results []raw `json:"results"`
	}
	q := url.Values{"search": {query}, "per-page": {fmt.Sprint(min(max(n, 1), 25))}, "select": {fields}}
	if err := c.get(ctx, "/works?", q, &body); err != nil {
		return nil, err
	}
	out := []Work{}
	for _, r := range body.Results {
		if w := r.work(); w.Title != "" {
			out = append(out, w)
		}
	}
	return out, nil
}

// ByDOI looks up one work.
func (c *Client) ByDOI(ctx context.Context, doi string) (*Work, error) {
	var r raw
	if err := c.get(ctx, "/works/doi:"+url.PathEscape(doi)+"?", url.Values{"select": {fields}}, &r); err != nil {
		return nil, err
	}
	w := r.work()
	return &w, nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, v any) error {
	if c.APIKey != "" {
		q.Set("api_key", c.APIKey)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+path+q.Encode(), nil)
	if err != nil {
		// Never err itself: it quotes the URL, and the URL carries the key.
		return errors.New("OpenAlex: the request could not be built")
	}
	req.Header.Set("User-Agent", "Refract (https://github.com/ponytojas/Refract)")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// A *url.Error prints the whole URL, api_key included: keep its cause only.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("OpenAlex is unreachable: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errors.New("OpenAlex has no such work")
	case resp.StatusCode == http.StatusTooManyRequests:
		return errors.New("OpenAlex's free daily budget is used up; set REFRACT_OPENALEX_API_KEY (a free key) for more")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("OpenAlex answered HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

type raw struct {
	ID          string `json:"id"`
	DOI         string `json:"doi"`
	Title       string `json:"display_name"`
	Year        int    `json:"publication_year"`
	CitedBy     int    `json:"cited_by_count"`
	Authorships []struct {
		Author struct {
			Name string `json:"display_name"`
		} `json:"author"`
	} `json:"authorships"`
	Location *struct {
		Source *struct {
			Name string `json:"display_name"`
		} `json:"source"`
		RawSource string `json:"raw_source_name"`
	} `json:"primary_location"`
	Abstract map[string][]int `json:"abstract_inverted_index"`
}

func (r raw) work() Work {
	w := Work{ID: strings.TrimPrefix(r.ID, "https://openalex.org/"), DOI: NormalizeDOI(r.DOI), Title: strings.Join(strings.Fields(r.Title), " "), Year: r.Year, CitedBy: r.CitedBy}
	for _, a := range r.Authorships {
		w.Authors = append(w.Authors, a.Author.Name)
	}
	if l := r.Location; l != nil {
		w.Venue = l.RawSource
		if l.Source != nil && l.Source.Name != "" {
			w.Venue = l.Source.Name
		}
	}
	w.Abstract = abstract(r.Abstract)
	return w
}

// abstract rebuilds the text OpenAlex ships as word → positions.
func abstract(index map[string][]int) string {
	type at struct {
		pos  int
		word string
	}
	var words []at
	for w, ps := range index {
		for _, p := range ps {
			words = append(words, at{p, w})
		}
	}
	slices.SortFunc(words, func(a, b at) int { return a.pos - b.pos })
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = w.word
	}
	return strings.Join(out, " ")
}

var doiPattern = regexp.MustCompile(`^10\.\d{4,9}/\S+$`)

// NormalizeDOI turns "https://doi.org/10.1/X", "doi:10.1/X" or "10.1/X" into
// "10.1/x"; it returns "" for anything else.
func NormalizeDOI(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range []string{"https://doi.org/", "http://doi.org/", "https://dx.doi.org/", "http://dx.doi.org/", "doi:", "DOI:"} {
		s = strings.TrimPrefix(s, p)
	}
	s = strings.ToLower(s)
	if !doiPattern.MatchString(s) {
		return ""
	}
	return s
}

// Ref makes a work citable. Its BibTeX comes from doi.org; when doi.org has
// none, the entry is built from OpenAlex's own fields.
func (c *Client) Ref(ctx context.Context, w Work) refs.Ref {
	entry := ""
	if w.DOI != "" {
		entry, _ = c.bibtex(ctx, w.DOI)
	}
	return refs.Ref{
		Title: w.Title, Authors: w.Authors, Year: w.Year, DOI: w.DOI,
		BibTeX: func(key string) string {
			if entry != "" {
				return rekey(entry, key)
			}
			return fallback(w, key)
		},
	}
}

func (c *Client) bibtex(ctx context.Context, doi string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.DOIURL+"/"+doi, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/x-bibtex; charset=utf-8")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("doi.org answered HTTP %d", resp.StatusCode)
	}
	s := strings.TrimSpace(string(b))
	if !entryStart.MatchString(s) {
		return "", errors.New("doi.org did not return BibTeX")
	}
	return pretty(s), nil
}

var entryStart = regexp.MustCompile(`^@\w+\s*\{\s*[^,\s]*\s*,`)

// rekey gives an entry our citation key.
func rekey(entry, key string) string {
	m := regexp.MustCompile(`^@(\w+)`).FindStringSubmatch(entry)
	return "@" + m[1] + "{" + key + "," + entry[len(entryStart.FindString(entry)):]
}

// pretty puts each top-level field of a one-line entry on its own line.
func pretty(entry string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(entry); i++ {
		c := entry[i]
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				s := strings.TrimRight(b.String(), " ,")
				b.Reset()
				b.WriteString(s + ",\n}\n")
				continue
			}
		case ',':
			if depth == 1 {
				b.WriteString(",\n  ")
				for i+1 < len(entry) && entry[i+1] == ' ' {
					i++
				}
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

func fallback(w Work, key string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "@article{%s,\n", key)
	field := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&b, "  %s = {%s},\n", name, value)
		}
	}
	field("title", refs.Escape(w.Title))
	field("author", refs.Escape(strings.Join(w.Authors, " and ")))
	if w.Year > 0 {
		field("year", fmt.Sprint(w.Year))
	}
	field("journal", refs.Escape(w.Venue))
	field("doi", w.DOI)
	b.WriteString("}\n")
	return b.String()
}
