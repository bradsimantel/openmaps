package scout

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixture(t *testing.T, kind string) (string, *Client) {
	t.Helper()
	root := t.TempDir()
	for name := range metadata {
		b, e := os.ReadFile(filepath.Join("testdata", kind, name))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(root, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	c := NewClient()
	c.Free = func(string) (int64, error) { return 100 * GiB, nil }
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		path := strings.TrimPrefix(r.URL.String(), Base)
		name := path
		if path == "countries_provided.json" {
			name = "catalog.json"
		}
		if path == Prefix {
			name = "packages.html"
		} else if strings.HasPrefix(path, Prefix) {
			id := strings.TrimPrefix(path, Prefix)
			if strings.HasSuffix(id, ".tar.bz2") {
				name = strings.TrimSuffix(id, ".tar.bz2") + ".payload"
			} else {
				name = strings.TrimSuffix(id, ".tar.size-compressed") + ".size"
			}
		}
		b, e := os.ReadFile(filepath.Join("testdata", kind, name))
		if e != nil {
			return nil, e
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(b)), Body: io.NopCloser(strings.NewReader(string(b))), Header: http.Header{}}, nil
	})
	return root, c
}
func planFixture(t *testing.T) (string, *Client, Plan) {
	t.Helper()
	root, c := fixture(t, "valid")
	p, e := c.MakePlan(context.Background(), root, []string{"test"}, []string{"3"}, Budgets{GiB, 32 * GiB})
	if e != nil {
		t.Fatal(e)
	}
	return root, c, p
}
func TestCompleteManifestAndGeneration(t *testing.T) {
	root, c, p := planFixture(t)
	if p.FullManifestPackages != 3 || len(p.CatalogOmitted) != 1 || p.CatalogOmitted[0] != "3" || len(p.Packages) != 3 {
		t.Fatal(p)
	}
	if e := ValidatePlan(root, p, p.Budgets); e != nil {
		t.Fatal(e)
	}
	root, _ = fixture(t, "conflict")
	if _, e := LoadManifest(root); e == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	root, c = fixture(t, "mixed")
	if _, e := c.MakePlan(context.Background(), root, []string{"test"}, nil, p.Budgets); e == nil {
		t.Fatal("mixed generation accepted")
	}
	root, _ = fixture(t, "valid")
	os.WriteFile(filepath.Join(root, "packages.html"), []byte(`<a href="1.tar.bz2">one</a>`), 0600)
	if _, e := LoadManifest(root); e == nil {
		t.Fatal("incomplete directory accepted")
	}
}
func TestResumeAndImmutableReceipt(t *testing.T) {
	root, c, p := planFixture(t)
	ctx := context.Background()
	original := c.HTTP.Transport
	downloads := 0
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, ".tar.bz2") {
			downloads++
		}
		return original.RoundTrip(r)
	})
	if e := c.Fetch(ctx, root, p, p.Budgets, nil); e != nil {
		t.Fatal(e)
	}
	if downloads != 3 {
		t.Fatal(downloads)
	}
	if e := c.Fetch(ctx, root, p, p.Budgets, nil); e != nil {
		t.Fatal(e)
	}
	if downloads != 3 {
		t.Fatal("resume redownloaded", downloads)
	}
	if e := VerifyRetained(root, p, p.Budgets); e != nil {
		t.Fatal(e)
	}
	receiptPath := filepath.Join(root, "receipts", "1.json")
	before, _ := os.ReadFile(receiptPath)
	p.Packages[0].SHA256 = strings.Repeat("0", 64)
	if e := c.Fetch(ctx, root, p, p.Budgets, nil); e == nil {
		t.Fatal("bad resumed SHA accepted")
	}
	after, _ := os.ReadFile(receiptPath)
	if string(before) != string(after) {
		t.Fatal("receipt overwritten")
	}
	p.Packages[0].SHA256 = ""
	os.WriteFile(receiptPath, []byte(`{}`), 0600)
	if e := c.Fetch(ctx, root, p, p.Budgets, nil); e == nil {
		t.Fatal("conflicting receipt accepted")
	}
}
func TestDownloadFailureRetryAndBudgets(t *testing.T) {
	root, c, p := planFixture(t)
	original := c.HTTP.Transport
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		res, e := original.RoundTrip(r)
		if e == nil && strings.HasSuffix(r.URL.Path, "1.tar.bz2") {
			res.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", int(p.Packages[0].Bytes))))
		}
		return res, e
	})
	if e := c.Fetch(context.Background(), root, p, p.Budgets, nil); e == nil {
		t.Fatal("checksum failure accepted")
	}
	if _, e := os.Stat(filepath.Join(root, "packages", "1.tar.bz2")); !os.IsNotExist(e) {
		t.Fatal("bad package published")
	}
	c.HTTP.Transport = original
	if e := c.Fetch(context.Background(), root, p, p.Budgets, nil); e != nil {
		t.Fatal(e)
	}
	root, c, p = planFixture(t)
	c.Free = func(string) (int64, error) { return 32 * GiB, nil }
	if e := c.Fetch(context.Background(), root, p, p.Budgets, nil); e == nil {
		t.Fatal("disk reserve ignored")
	}
	root, c, p = planFixture(t)
	p.Packages = append(p.Packages, p.Packages[0])
	c.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid plan reached network")
		return nil, errors.New("unexpected")
	})
	if e := c.Fetch(context.Background(), root, p, p.Budgets, nil); e == nil {
		t.Fatal("duplicate package accepted")
	}
	root, c, p = planFixture(t)
	p.Budgets.Download = 13 * GiB
	if e := c.Fetch(context.Background(), root, p, Budgets{12 * GiB, 32 * GiB}, nil); e == nil {
		t.Fatal("pinned budget escalation accepted")
	}
}
func TestGenerationRechecksAndStreamDiskBudget(t *testing.T) {
	root, c, p := planFixture(t)
	original := c.HTTP.Transport
	catalogRequests := 0
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		res, e := original.RoundTrip(r)
		if strings.HasSuffix(r.URL.Path, "countries_provided.json") {
			catalogRequests++
			if catalogRequests == 2 {
				res.Body = io.NopCloser(strings.NewReader("{}"))
			}
		}
		return res, e
	})
	if e := c.Fetch(context.Background(), root, p, p.Budgets, nil); e == nil || !strings.Contains(e.Error(), "generation changed") {
		t.Fatal(e)
	}
	root, c, p = planFixture(t)
	calls := 0
	c.Free = func(string) (int64, error) {
		calls++
		if calls > 1 {
			return 32 * GiB, nil
		}
		return 100 * GiB, nil
	}
	if e := c.Fetch(context.Background(), root, p, p.Budgets, nil); e == nil {
		t.Fatal("stream budget ignored")
	}
	if _, e := bounded(strings.NewReader("12345"), 4); e == nil {
		t.Fatal("unbounded metadata")
	}
}
func TestPinnedNationalInputs(t *testing.T) {
	root := os.Getenv("OPENMAPS_SCOUT_ACQUISITION")
	if root == "" {
		t.Skip("downloaded-data check; set OPENMAPS_SCOUT_ACQUISITION")
	}
	p, e := ReadPlan(root, "national-aleutian-acquisition.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = VerifyRetained(root, p, Budgets{16 * GiB, 32 * GiB}); e != nil {
		t.Fatal(e)
	}
}

func TestMetadataSnapshotIsImmutable(t *testing.T) {
	_, c := fixture(t, "valid")
	root := filepath.Join(t.TempDir(), "capture")
	if e := c.Snapshot(context.Background(), root); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadManifest(root); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(root, "catalog.json"))
	if e := c.Snapshot(context.Background(), root); e == nil {
		t.Fatal("replaced existing metadata generation")
	}
	after, _ := os.ReadFile(filepath.Join(root, "catalog.json"))
	if string(before) != string(after) {
		t.Fatal("metadata changed")
	}
}

type interruptedBody struct{ first bool }

func (r *interruptedBody) Read(b []byte) (int, error) {
	if r.first {
		return 0, io.ErrUnexpectedEOF
	}
	r.first = true
	return copy(b, []byte("part")), nil
}
func (*interruptedBody) Close() error { return nil }
func TestInterruptedFetchResumesOnlyIncompletePackage(t *testing.T) {
	root, c, p := planFixture(t)
	original := c.HTTP.Transport
	downloads := map[string]int{}
	interrupt := true
	c.HTTP.Transport = transport(func(r *http.Request) (*http.Response, error) {
		res, e := original.RoundTrip(r)
		if strings.HasSuffix(r.URL.Path, ".tar.bz2") {
			downloads[r.URL.Path]++
			if interrupt && strings.HasSuffix(r.URL.Path, "/2.tar.bz2") {
				res.Body = &interruptedBody{}
			}
		}
		return res, e
	})
	if e := c.Fetch(context.Background(), root, p, p.Budgets, nil); !errors.Is(e, io.ErrUnexpectedEOF) {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(root, "packages", "2.tar.partial")); e != nil {
		t.Fatal("interrupted file not retained", e)
	}
	interrupt = false
	if e := c.Fetch(context.Background(), root, p, p.Budgets, nil); e != nil {
		t.Fatal(e)
	}
	for id, want := range map[string]int{"1": 1, "2": 2, "3": 1} {
		if downloads["/osm_scout_server/"+Prefix+id+".tar.bz2"] != want {
			t.Fatal(downloads)
		}
	}
}
