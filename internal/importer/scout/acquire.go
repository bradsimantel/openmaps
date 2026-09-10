// Package scout acquires pinned provider packages without interpreting road data.
// Complete manifests establish byte consistency, not an authenticated OSM cutoff.
package scout

import (
	"bytes"
	"compress/bzip2"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const Base = "https://data.modrana.org/osm_scout_server/"
const Prefix = "valhalla-34/valhalla/packages/"
const MiB int64 = 1 << 20
const GiB int64 = 1 << 30

var metadata = map[string]string{"catalog.json": "countries_provided.json", "digest.md5.bz2": "digest.md5.bz2", "packages.html": Prefix}
var digits = regexp.MustCompile(`^[0-9]+$`)
var packagePath = regexp.MustCompile(`^` + regexp.QuoteMeta(Prefix) + `([0-9]+)\.tar\.bz2$`)
var listingLink = regexp.MustCompile(`href="([0-9]+)\.tar\.bz2"`)

type Package struct {
	ID     string    `json:"id"`
	Bytes  int64     `json:"bytes"`
	MD5    string    `json:"md5"`
	SHA256 string    `json:"sha256,omitempty"`
	URL    string    `json:"url"`
	Tiles  []TilePin `json:"tiles,omitempty"`
}

// TilePin retains optional member constraints for graph preparation. Acquisition
// carries these pins without interpreting the provider's road records.
type TilePin struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type Budgets struct {
	Download int64 `json:"download_bytes"`
	Reserve  int64 `json:"disk_reserve_bytes"`
}
type Plan struct {
	Schema               int               `json:"schema"`
	PackageSchema        string            `json:"package_schema"`
	Version              string            `json:"tile_version"`
	Dataset              uint64            `json:"dataset_id,omitempty"`
	Timestamp            string            `json:"timestamp"`
	Packages             []Package         `json:"packages"`
	MetadataSHA256       map[string]string `json:"metadata_sha256"`
	FullManifestPackages int               `json:"full_manifest_packages"`
	CatalogOmitted       []string          `json:"catalog_omitted_packages"`
	Regions              []string          `json:"regions"`
	CompressedBytes      int64             `json:"compressed_bytes"`
	Budgets              Budgets           `json:"budgets"`
	Provenance           string            `json:"provenance"`
	Attribution          struct {
		Text string `json:"text"`
		URL  string `json:"url"`
	} `json:"attribution"`
}
type Receipt struct {
	Package
	Generation map[string]string `json:"generation"`
}
type Region struct {
	Packages  []string `json:"packages"`
	Timestamp string   `json:"timestamp"`
	Version   string   `json:"version"`
}
type Manifest struct {
	Entries  map[string]string
	Packages map[string]bool
	Catalog  map[string]Region
	Pins     map[string]string
}

// Client's transport and disk observation are boundaries used by offline fixtures.
// Package URLs always retain their canonical provider origin in plans and receipts.
type Client struct {
	HTTP *http.Client
	Free func(string) (int64, error)
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 60 * time.Second}, Free: FreeDisk}
}
func FreeDisk(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
func Read(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return bounded(f, limit)
}
func bounded(r io.Reader, limit int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, errors.New("metadata exceeds budget")
	}
	return b, nil
}
func sha(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func md(b []byte) string  { return fmt.Sprintf("%x", md5.Sum(b)) }
func digest(s string, n int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == n && s == strings.ToLower(s)
}

// Publish syncs a private sibling, then links without replacing any existing file.
func Publish(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".scout-publish-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Link(f.Name(), path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func WriteJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return Publish(path, append(b, '\n'))
}
func (c *Client) remote(ctx context.Context, path string, limit int64) ([]byte, error) {
	r, e := http.NewRequestWithContext(ctx, "GET", Base+path, nil)
	if e != nil {
		return nil, e
	}
	res, e := c.HTTP.Do(r)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("provider HTTP %d", res.StatusCode)
	}
	return bounded(res.Body, limit)
}
func LoadManifest(root string) (Manifest, error) {
	m := Manifest{Entries: map[string]string{}, Packages: map[string]bool{}, Catalog: map[string]Region{}, Pins: map[string]string{}}
	raw := map[string][]byte{}
	for name := range metadata {
		b, e := Read(filepath.Join(root, name), 16*MiB)
		if e != nil {
			return m, e
		}
		raw[name] = b
		m.Pins[name] = sha(b)
	}
	decoded, e := bounded(bzip2.NewReader(bytes.NewReader(raw["digest.md5.bz2"])), 32*MiB)
	if e != nil {
		return m, e
	}
	for _, line := range strings.Split(strings.TrimSpace(string(decoded)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || !digest(fields[0], 16) {
			return m, errors.New("invalid provider digest entry")
		}
		hash, path := fields[0], fields[2]
		if prior, ok := m.Entries[path]; ok && prior != hash {
			return m, errors.New("conflicting duplicate provider digest entry")
		}
		m.Entries[path] = hash
		if match := packagePath.FindStringSubmatch(path); match != nil {
			m.Packages[match[1]] = true
		}
	}
	listed := map[string]bool{}
	for _, v := range listingLink.FindAllSubmatch(raw["packages.html"], -1) {
		id := string(v[1])
		if listed[id] {
			return m, errors.New("duplicate directory package")
		}
		listed[id] = true
	}
	if len(m.Packages) == 0 || len(m.Packages) > 4096 || !reflect.DeepEqual(listed, m.Packages) {
		return m, errors.New("directory/full digest package inventory mismatch")
	}
	if md(raw["catalog.json"]) != m.Entries["countries_provided.json"] {
		return m, errors.New("catalog/full digest mismatch")
	}
	var catalog map[string]struct {
		Valhalla json.RawMessage `json:"valhalla"`
	}
	if e = json.Unmarshal(raw["catalog.json"], &catalog); e != nil {
		return m, e
	}
	for name, value := range catalog {
		v := bytes.TrimSpace(value.Valhalla)
		if len(v) == 0 || v[0] != '{' {
			continue
		}
		var r Region
		if e = json.Unmarshal(v, &r); e != nil {
			return m, e
		}
		m.Catalog[name] = r
	}
	return m, nil
}
func (c *Client) Snapshot(ctx context.Context, root string) error {
	if e := os.Mkdir(root, 0700); e != nil {
		return e
	}
	for name, remote := range metadata {
		b, e := c.remote(ctx, remote, 16*MiB)
		if e != nil {
			return e
		}
		if e = Publish(filepath.Join(root, name), b); e != nil {
			return e
		}
	}
	_, e := LoadManifest(root)
	return e
}
func sorted(set map[string]bool) []string {
	v := make([]string, 0, len(set))
	for k := range set {
		v = append(v, k)
	}
	sort.Slice(v, func(i, j int) bool {
		if len(v[i]) != len(v[j]) {
			return len(v[i]) < len(v[j])
		}
		return v[i] < v[j]
	})
	return v
}
func (c *Client) MakePlan(ctx context.Context, root string, regions, extra []string, budget Budgets) (Plan, error) {
	p := Plan{Schema: 1, PackageSchema: "2", Version: "3.4.0", Budgets: budget}
	if budget.Download < 1 || budget.Download > 16*GiB || budget.Reserve < 32*GiB || budget.Reserve > 1024*GiB {
		return p, errors.New("invalid acquisition budgets")
	}
	m, e := LoadManifest(root)
	if e != nil {
		return p, e
	}
	selected := map[string]bool{}
	union := map[string]bool{}
	for _, id := range extra {
		if id != "" {
			selected[id] = true
		}
	}
	for name, r := range m.Catalog {
		for _, id := range r.Packages {
			union[id] = true
		}
		match := false
		for _, prefix := range regions {
			if prefix != "" && (name == prefix || strings.HasPrefix(name, prefix+"/")) {
				match = true
			}
		}
		if !match {
			continue
		}
		if r.Timestamp == "" || r.Version != "2" || p.Timestamp != "" && p.Timestamp != r.Timestamp {
			return p, errors.New("mixed or unsupported catalog generation")
		}
		p.Timestamp = r.Timestamp
		p.Regions = append(p.Regions, name)
		for _, id := range r.Packages {
			selected[id] = true
		}
	}
	if len(p.Regions) == 0 {
		return p, errors.New("no matching regional selections")
	}
	sort.Strings(p.Regions)
	for _, id := range sorted(selected) {
		if !m.Packages[id] {
			return p, errors.New("selection absent from complete provider manifest")
		}
		path := Prefix + id + ".tar.size-compressed"
		b, e := c.remote(ctx, path, 4096)
		if e != nil {
			return p, e
		}
		if md(b) != m.Entries[path] {
			return p, errors.New("size sidecar digest mismatch")
		}
		v := strings.Fields(string(b))
		if len(v) != 2 || v[1] != "valhalla/packages/"+id+".tar.bz2" {
			return p, errors.New("size sidecar names wrong package")
		}
		n, e := strconv.ParseInt(v[0], 10, 64)
		if e != nil || n < 1 || n > 256*MiB {
			return p, errors.New("compressed package exceeds per-package budget")
		}
		p.Packages = append(p.Packages, Package{ID: id, Bytes: n, MD5: m.Entries[Prefix+id+".tar.bz2"], URL: Base + Prefix + id + ".tar.bz2"})
		p.CompressedBytes += n
	}
	omitted := map[string]bool{}
	for id := range m.Packages {
		if !union[id] {
			omitted[id] = true
		}
	}
	p.CatalogOmitted = sorted(omitted)
	p.MetadataSHA256 = m.Pins
	p.FullManifestPackages = len(m.Packages)
	free, e := c.Free(root)
	if e != nil {
		return p, e
	}
	if p.CompressedBytes > budget.Download || free-p.CompressedBytes < budget.Reserve {
		return p, errors.New("acquisition budget rejected")
	}
	p.Provenance = "Provider-byte consistency only; exact OSM cutoff and production build inputs are unverified."
	p.Attribution.Text = "© OpenStreetMap contributors"
	p.Attribution.URL = "https://www.openstreetmap.org/copyright"
	return p, nil
}

// ValidatePlan checks the entire local inventory and generation before network or writes.
func ValidatePlan(root string, p Plan, ceiling Budgets) error {
	if p.Schema != 1 || p.PackageSchema != "2" || p.Version != "3.4.0" || p.Timestamp == "" || len(p.Packages) < 1 || len(p.Packages) > 4096 || p.Budgets.Download < 1 || p.Budgets.Download > ceiling.Download || p.Budgets.Download > 16*GiB || p.Budgets.Reserve < ceiling.Reserve || p.Budgets.Reserve < 32*GiB || p.Budgets.Reserve > 1024*GiB {
		return errors.New("invalid pinned acquisition budgets or schema")
	}
	m, e := LoadManifest(root)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(p.MetadataSHA256, m.Pins) {
		return errors.New("local manifest changed or incomplete metadata pins")
	}
	if p.FullManifestPackages != len(m.Packages) {
		return errors.New("full manifest count mismatch")
	}
	union := map[string]bool{}
	for _, r := range m.Catalog {
		for _, id := range r.Packages {
			union[id] = true
		}
	}
	omitted := map[string]bool{}
	for id := range m.Packages {
		if !union[id] {
			omitted[id] = true
		}
	}
	if !reflect.DeepEqual(sorted(omitted), p.CatalogOmitted) {
		return errors.New("catalog omissions mismatch")
	}
	seen := map[string]bool{}
	var total int64
	for _, pin := range p.Packages {
		if !digits.MatchString(pin.ID) || seen[pin.ID] || pin.Bytes < 1 || pin.Bytes > 256*MiB || !digest(pin.MD5, 16) || pin.SHA256 != "" && !digest(pin.SHA256, 32) || pin.URL != Base+Prefix+pin.ID+".tar.bz2" || pin.MD5 != m.Entries[Prefix+pin.ID+".tar.bz2"] {
			return errors.New("invalid/duplicate provider package pin")
		}
		seen[pin.ID] = true
		total += pin.Bytes
	}
	if total != p.CompressedBytes || total > p.Budgets.Download {
		return errors.New("plan exceeds download budget or total mismatch")
	}
	if len(p.Regions) == 0 {
		return errors.New("regional generation evidence required")
	}
	regionSeen := map[string]bool{}
	for _, name := range p.Regions {
		r, ok := m.Catalog[name]
		if !ok || regionSeen[name] || r.Timestamp != p.Timestamp || r.Version != p.PackageSchema {
			return errors.New("mixed or unsupported catalog generation")
		}
		regionSeen[name] = true
		for _, id := range r.Packages {
			if !seen[id] {
				return errors.New("plan omits selected regional package")
			}
		}
	}
	return nil
}
func ReadPlan(root, name string) (Plan, error) {
	var p Plan
	if filepath.Base(name) != name {
		return p, errors.New("invalid plan filename")
	}
	b, e := Read(filepath.Join(root, name), 8*MiB)
	if e == nil {
		e = json.Unmarshal(b, &p)
	}
	return p, e
}
func (c *Client) recheck(ctx context.Context, p Plan) error {
	for _, name := range []string{"catalog.json", "digest.md5.bz2"} {
		b, e := c.remote(ctx, metadata[name], 16*MiB)
		if e != nil {
			return e
		}
		if sha(b) != p.MetadataSHA256[name] {
			return errors.New("provider generation changed: " + name)
		}
	}
	return nil
}
func hashes(path string) (string, string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", "", e
	}
	defer f.Close()
	a, b := md5.New(), sha256.New()
	_, e = io.Copy(io.MultiWriter(a, b), f)
	return hex.EncodeToString(a.Sum(nil)), hex.EncodeToString(b.Sum(nil)), e
}
func checkFile(path string, p Package) (string, error) {
	st, e := os.Stat(path)
	if e != nil {
		return "", e
	}
	if st.Size() != p.Bytes {
		return "", errors.New("retained package size mismatch")
	}
	a, b, e := hashes(path)
	if e != nil {
		return "", e
	}
	if a != p.MD5 || p.SHA256 != "" && b != p.SHA256 {
		return "", errors.New("retained package checksum mismatch")
	}
	return b, nil
}

// VerifyRetained is entirely offline and validates receipts and every selected byte.
func VerifyRetained(root string, p Plan, ceiling Budgets) error {
	if e := ValidatePlan(root, p, ceiling); e != nil {
		return e
	}
	for _, pin := range p.Packages {
		sum, e := checkFile(filepath.Join(root, "packages", pin.ID+".tar.bz2"), pin)
		if e != nil {
			return e
		}
		pin.SHA256 = sum
		if _, e = receipt(root, pin, p.MetadataSHA256, false); e != nil {
			return e
		}
	}
	return nil
}
func receipt(root string, p Package, generation map[string]string, create bool) (Package, error) {
	path := filepath.Join(root, "receipts", p.ID+".json")
	want := Receipt{p, generation}
	b, e := Read(path, 65536)
	if os.IsNotExist(e) && create {
		return p, WriteJSON(path, want)
	}
	if e != nil {
		return Package{}, e
	}
	var got Receipt
	if e = json.Unmarshal(b, &got); e != nil {
		return Package{}, e
	}
	if got.ID != p.ID || got.Bytes != p.Bytes || got.MD5 != p.MD5 || got.URL != p.URL ||
		!digest(got.SHA256, 32) || p.SHA256 != "" && p.SHA256 != got.SHA256 ||
		!reflect.DeepEqual(got.Generation, generation) {
		return Package{}, errors.New("existing receipt conflicts with pinned generation")
	}
	if len(p.Tiles) > 0 && len(got.Tiles) > 0 && !reflect.DeepEqual(p.Tiles, got.Tiles) {
		return Package{}, errors.New("receipt conflicts with explicit tile pins")
	}
	p.SHA256 = got.SHA256
	if len(p.Tiles) == 0 {
		p.Tiles = got.Tiles
	}
	return p, nil
}
func (c *Client) Fetch(ctx context.Context, root string, p Plan, ceiling Budgets, progress io.Writer) error {
	if e := ValidatePlan(root, p, ceiling); e != nil {
		return e
	}
	if e := c.recheck(ctx, p); e != nil {
		return e
	}
	for _, dir := range []string{"packages", "receipts"} {
		if e := os.MkdirAll(filepath.Join(root, dir), 0700); e != nil {
			return e
		}
	}
	for _, pin := range p.Packages {
		if e := ctx.Err(); e != nil {
			return e
		}
		target := filepath.Join(root, "packages", pin.ID+".tar.bz2")
		if _, e := os.Stat(target); os.IsNotExist(e) {
			if e = c.download(ctx, root, target, pin, p.Budgets.Reserve); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		sum, e := checkFile(target, pin)
		if e != nil {
			return e
		}
		pin.SHA256 = sum
		if _, e = receipt(root, pin, p.MetadataSHA256, true); e != nil {
			return e
		}
		if progress != nil {
			fmt.Fprintln(progress, "verified", pin.ID, pin.Bytes, sum)
		}
	}
	return c.recheck(ctx, p)
}
func (c *Client) download(ctx context.Context, root, target string, p Package, reserve int64) error {
	free, e := c.Free(root)
	if e != nil {
		return e
	}
	if free-p.Bytes < reserve {
		return errors.New("disk reserve would be crossed before package " + p.ID)
	}
	// Only this command's incomplete transfer may be retried. Published inputs are immutable.
	partial := strings.TrimSuffix(target, ".bz2") + ".partial"
	if e = os.Remove(partial); e != nil && !os.IsNotExist(e) {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", p.URL, nil)
	if e != nil {
		return e
	}
	res, e := c.HTTP.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.ContentLength != p.Bytes {
		return errors.New("remote package size/status changed")
	}
	f, e := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	var total int64
	buffer := make([]byte, MiB)
	for {
		n, readErr := res.Body.Read(buffer[:min(int64(len(buffer)), p.Bytes-total+1)])
		if n > 0 {
			total += int64(n)
			free, e = c.Free(root)
			if e != nil {
				return e
			}
			if total > p.Bytes || free-int64(n) < reserve {
				return errors.New("stream/disk budget exceeded")
			}
			if _, e = f.Write(buffer[:n]); e != nil {
				return e
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
		if e = ctx.Err(); e != nil {
			return e
		}
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if _, e = checkFile(partial, p); e != nil {
		return e
	}
	if e = os.Link(partial, target); e != nil {
		return e
	}
	if e = os.Remove(partial); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(target))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
