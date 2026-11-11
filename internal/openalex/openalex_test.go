package openalex

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/refs"
)

// server answers like OpenAlex and doi.org, from real responses in testdata.
func server(t *testing.T) (*Client, *[]string) {
	t.Helper()
	search, _ := os.ReadFile("testdata/search.json")
	bib, _ := os.ReadFile("testdata/lecun.bib")
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.String())
		switch {
		case r.URL.Path == "/works":
			w.Write(search)
		case r.URL.Path == "/doi/10.1038/nature14539" && strings.Contains(r.Header.Get("Accept"), "x-bibtex"):
			w.Write(bib)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), BaseURL: srv.URL, DOIURL: srv.URL + "/doi", APIKey: "k"}, &seen
}

func TestSearch(t *testing.T) {
	c, seen := server(t)
	ws, err := c.Search(context.Background(), "transformer attention", 3)
	if err != nil || len(ws) != 3 {
		t.Fatalf("search = %v, %v", ws, err)
	}
	w := ws[0]
	if w.DOI != "10.18653/v1/2021.acl-long.103" || w.Year != 2021 || len(w.Authors) == 0 || w.Authors[0] != "Yu Lu" || !strings.HasPrefix(w.Venue, "Proceedings of the 59th") {
		t.Errorf("work = %+v", w)
	}
	if ws[1].Venue != "Conference on Empirical Methods in Natural Language Processing (EMNLP)" {
		t.Errorf("venue from source = %q", ws[1].Venue)
	}
	if !strings.HasPrefix(w.Abstract, "Yu Lu, Jiali Zeng") { // positions put words back in order
		t.Errorf("abstract = %.60q", w.Abstract)
	}
	if !strings.Contains((*seen)[0], "api_key=k") || !strings.Contains((*seen)[0], "search=transformer+attention") {
		t.Errorf("request = %s", (*seen)[0])
	}
}

func TestNormalizeDOI(t *testing.T) {
	for in, want := range map[string]string{
		"https://doi.org/10.1038/Nature14539": "10.1038/nature14539",
		"doi:10.18653/v1/2021.acl-long.103":   "10.18653/v1/2021.acl-long.103",
		" 10.1145/3292500.3330701 ":           "10.1145/3292500.3330701",
		"not a doi":                           "",
		"10.1/x y":                            "",
	} {
		if got := NormalizeDOI(in); got != want {
			t.Errorf("NormalizeDOI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRefUsesDOIBibTeX(t *testing.T) {
	c, _ := server(t)
	w := Work{Title: "Deep learning", Authors: []string{"Yann LeCun"}, Year: 2015, DOI: "10.1038/nature14539", Venue: "Nature"}
	r := c.Ref(context.Background(), w)
	if k := refs.Key(r); k != "lecun2015deep" {
		t.Errorf("key = %q", k)
	}
	b := r.BibTeX("lecun2015deep")
	if !strings.HasPrefix(b, "@article{lecun2015deep,\n  title={Deep learning},\n") || !strings.Contains(b, "journal={Nature}") || !strings.HasSuffix(b, "pages={436–444},\n}\n") {
		t.Errorf("bibtex =\n%s", b)
	}
	// No BibTeX at doi.org: built from OpenAlex's fields.
	w.DOI = "10.9999/unknown"
	b = c.Ref(context.Background(), w).BibTeX("k")
	if !strings.HasPrefix(b, "@article{k,\n") || !strings.Contains(b, "doi = {10.9999/unknown}") || !strings.Contains(b, "journal = {Nature}") {
		t.Errorf("fallback =\n%s", b)
	}
}

// The key travels in the query string, and transport errors quote the URL:
// none of that may reach the error text shown to users and models.
func TestErrorsNeverCarryTheKey(t *testing.T) {
	const key = "openalex-test-key-1234567890"
	srv := httptest.NewServer(http.NotFoundHandler())
	closed := srv.URL
	srv.Close() // every request now fails at the transport
	c := &Client{HTTP: &http.Client{Timeout: 2 * time.Second}, BaseURL: closed, DOIURL: closed + "/doi", APIKey: key}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	calls := map[string]func() error{
		"search": func() error { _, err := c.Search(context.Background(), "attention", 5); return err },
		"doi":    func() error { _, err := c.ByDOI(context.Background(), "10.1000/x"); return err },
		"cancel": func() error { _, err := c.Search(cancelled, "attention", 5); return err },
	}
	for name, call := range calls {
		err := call()
		if err == nil {
			t.Fatalf("%s: no error from a closed server", name)
		}
		msg := err.Error()
		if !strings.Contains(msg, "OpenAlex is unreachable") || strings.Contains(msg, key) || strings.Contains(msg, "api_key") {
			t.Errorf("%s: error = %q", name, msg)
		}
	}
}

// Some causes quote the URL themselves: a redirect whose Location can't be
// parsed is reported with the Location, and a server that keeps the query
// string in it hands the key back.
func TestRedirectErrorsNeverCarryTheKey(t *testing.T) {
	const key = "openalex-test-key-1234567890"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://%zz/works?"+r.URL.RawQuery)
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, DOIURL: srv.URL + "/doi", APIKey: key}
	_, err := c.Search(context.Background(), "attention", 5)
	if err == nil {
		t.Fatal("no error from a broken redirect")
	}
	if msg := err.Error(); !strings.Contains(msg, "OpenAlex is unreachable") || strings.Contains(msg, key) || strings.Contains(msg, "api_key") {
		t.Errorf("error = %q", msg)
	}
}

// Any cause may quote the URL: the error text never comes from it.
type quotingTransport struct{ inBody bool }

func (q quotingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !q.inBody {
		return nil, fmt.Errorf("dial %s: refused", r.URL)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(&quotingReader{r.URL.String()}), Request: r}, nil
}

type quotingReader struct{ url string }

func (q *quotingReader) Read([]byte) (int, error) { return 0, fmt.Errorf("reading %s: reset", q.url) }

func TestNoCauseCarriesTheKey(t *testing.T) {
	const key = "openalex-test-key-1234567890"
	for _, inBody := range []bool{false, true} {
		c := &Client{HTTP: &http.Client{Transport: quotingTransport{inBody}}, BaseURL: "https://openalex.invalid", APIKey: key}
		_, err := c.Search(context.Background(), "attention", 5)
		if err == nil {
			t.Fatalf("inBody=%v: no error", inBody)
		}
		if msg := err.Error(); strings.Contains(msg, key) || strings.Contains(msg, "api_key") || !strings.HasPrefix(msg, "OpenAlex ") {
			t.Errorf("inBody=%v: error = %q", inBody, msg)
		}
	}
}
