package valhallatiles

// Landmark vectors describe shortest costs in the relaxed directed node graph:
// ordinary permitted edges and zero-cost level transitions, ignoring turns and
// node access. Removing constraints cannot increase the optimum. Queries keep
// their full constraints and use only conservative triangle lower bounds.
import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const maxLandmarkNodes = 128000000
const maxLandmarks = 16
const landmarkSchema = "openmaps-scout-landmarks-v2"

type LandmarkSeed struct {
	Name  string `json:"name"`
	Point Point  `json:"point"`
}
type denseTile struct {
	ID           ID
	First, Count uint32
}
type denseNodes struct {
	tiles  []denseTile
	byTile map[ID]denseTile
	owners []uint32
	count  uint32
}

func newDenseNodes(r *Reader) (*denseNodes, error) {
	d := &denseNodes{byTile: map[ID]denseTile{}}
	for _, id := range r.TileIDs() {
		t, err := r.get(id)
		if err != nil {
			return nil, err
		}
		if t.nodes == 0 {
			continue
		}
		if uint64(d.count)+uint64(t.nodes) > maxLandmarkNodes {
			return nil, errors.New("landmark scratch admission exceeds 128 million nodes")
		}
		entry := denseTile{id, d.count, uint32(t.nodes)}
		d.tiles = append(d.tiles, entry)
		d.byTile[id] = entry
		d.count += uint32(t.nodes)
	}
	d.owners = make([]uint32, (d.count+255)/256)
	tile := 0
	for i := range d.owners {
		for tile+1 < len(d.tiles) && d.tiles[tile+1].First <= uint32(i)*256 {
			tile++
		}
		d.owners[i] = uint32(tile)
	}
	return d, nil
}
func (d *denseNodes) index(id ID) (uint32, error) {
	e, ok := d.byTile[id.Base()]
	if !ok {
		return 0, &MissingTileError{Tile: id.Base()}
	}
	if id.Index() < 0 || uint32(id.Index()) >= e.Count {
		return 0, errors.New("landmark node outside tile range")
	}
	return e.First + uint32(id.Index()), nil
}
func (d *denseNodes) id(index uint32) ID {
	i := d.owners[index/256]
	for i+1 < uint32(len(d.tiles)) && d.tiles[i+1].First <= index {
		i++
	}
	e := d.tiles[i]
	return e.ID.WithIndex(int(index - e.First))
}

type nodeHeap struct {
	nodes     []uint32
	positions []int32
	cost      []float64
}

func (h *nodeHeap) less(a, b uint32) bool {
	return h.cost[a] < h.cost[b] || h.cost[a] == h.cost[b] && a < b
}
func (h *nodeHeap) swap(i, j int) {
	h.nodes[i], h.nodes[j] = h.nodes[j], h.nodes[i]
	h.positions[h.nodes[i]] = int32(i + 1)
	h.positions[h.nodes[j]] = int32(j + 1)
}
func (h *nodeHeap) push(node uint32) {
	i := int(h.positions[node]) - 1
	if i < 0 {
		i = len(h.nodes)
		h.nodes = append(h.nodes, node)
		h.positions[node] = int32(i + 1)
	}
	for i > 0 {
		p := (i - 1) / 2
		if !h.less(h.nodes[i], h.nodes[p]) {
			break
		}
		h.swap(i, p)
		i = p
	}
}
func (h *nodeHeap) pop() uint32 {
	out := h.nodes[0]
	last := len(h.nodes) - 1
	h.swap(0, last)
	h.nodes = h.nodes[:last]
	h.positions[out] = -1
	for i := 0; ; {
		child := i*2 + 1
		if child >= len(h.nodes) {
			break
		}
		if child+1 < len(h.nodes) && h.less(h.nodes[child+1], h.nodes[child]) {
			child++
		}
		if !h.less(h.nodes[child], h.nodes[i]) {
			break
		}
		h.swap(i, child)
		i = child
	}
	return out
}

type landmarkVector struct {
	CumulativeCache CacheReport `json:"cumulative_cache"`
	File            string      `json:"file"`
	SHA256          string      `json:"sha256"`
	Bytes           int64       `json:"bytes"`
	SeedNode        ID          `json:"seed_node"`
	Reverse         bool        `json:"reverse"`
	Settled         uint64      `json:"settled"`
	Missing         uint64      `json:"missing_references"`
	Seconds         float64     `json:"seconds"`
}
type landmarkEntry struct {
	Seed             LandmarkSeed `json:"seed"`
	Forward, Reverse landmarkVector
}
type landmarkManifest struct {
	ReindexedFrom      string          `json:"reindexed_from,omitempty"`
	ExtensionSHA256    string          `json:"extension_sha256,omitempty"`
	Schema             string          `json:"schema"`
	GraphReceiptSHA256 string          `json:"graph_receipt_sha256"`
	Profile            string          `json:"profile"`
	NodeCount          uint32          `json:"node_count"`
	Entries            []landmarkEntry `json:"entries"`
}

// PrepareLandmarks is resumable one vector at a time. Each completed vector has
// a receipt bound to the same graph, profile, node order, seed and direction.
// Raw topology remains in tiles; scratch consists of distance/position arrays,
// one indexed heap, and the bounded graph cache. No routing graph is expanded.
func PrepareLandmarks(ctx context.Context, dir, out string, seeds []LandmarkSeed) error {
	if len(seeds) < 1 || len(seeds) > maxLandmarks {
		return errors.New("landmarks require 1..16 seeds")
	}
	for _, seed := range seeds {
		if seed.Name == "" || !seed.Point.valid() {
			return errors.New("invalid landmark seed")
		}
	}
	// National measurements at 128 MiB showed 24 million page loads for one
	// vector. Use the existing 256 MiB prepared-reader ceiling offline; query
	// and service cache limits remain independently bounded at 128 MiB.
	rtr, err := OpenPreparedRouter(ctx, dir, 256<<20)
	if err != nil {
		return err
	}
	defer rtr.Reader.Close()
	if err := rtr.loadReverseSupport(ctx, dir); err != nil {
		return err
	}
	d, err := newDenseNodes(rtr.Reader)
	if err != nil {
		return err
	}
	graph, err := readBoundedFile(filepath.Join(dir, "receipt.json"), maxPreparedIndex)
	if err != nil {
		return err
	}
	if hexSum(graph) != rtr.Reader.preparedSHA {
		return errors.New("graph receipt changed during preparation")
	}
	manifest := landmarkManifest{Schema: landmarkSchema, GraphReceiptSHA256: hexSum(graph), Profile: Profile, NodeCount: d.count}
	vectorBytes := (64 + int64(d.count)*4 + pageSize - 1) / pageSize * pageSize
	if err := os.Mkdir(out, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	var pending int64
	for i := range seeds {
		for _, reverse := range []bool{false, true} {
			if _, err := os.Stat(filepath.Join(out, fmt.Sprintf("landmark-%02d-%t.bin.json", i, reverse))); os.IsNotExist(err) {
				pending += vectorBytes
			} else if err != nil {
				return err
			}
		}
	}
	if err := diskReserve(out, pending, 32<<30); err != nil {
		return err
	}
	config := struct {
		Schema, Graph, Profile string
		Nodes                  uint32
		Seeds                  []LandmarkSeed
	}{landmarkSchema, manifest.GraphReceiptSHA256, Profile, d.count, seeds}
	configBytes, err := json.Marshal(config)
	if err != nil {
		return err
	}
	configPath := filepath.Join(out, "build.json")
	if prior, err := readBoundedFile(configPath, 1<<20); err == nil {
		var old any
		var want any
		if json.Unmarshal(prior, &old) != nil || json.Unmarshal(configBytes, &want) != nil {
			return errors.New("bad landmark build receipt")
		}
		a, _ := json.Marshal(old)
		b, _ := json.Marshal(want)
		if string(a) != string(b) {
			return errors.New("landmark build inputs changed")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		if err := publishJSON(out, "build.json", config); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(out, "landmarks.json")); err == nil {
		return errors.New("landmarks already published")
	}
	for i, seed := range seeds {
		if err := ctx.Err(); err != nil {
			return err
		}
		snap, err := rtr.SnapContext(ctx, seed.Point)
		if err != nil {
			return fmt.Errorf("landmark %s: %w", seed.Name, err)
		}
		dirs, err := rtr.directions(snap)
		if err != nil {
			return err
		}
		if len(dirs) == 0 {
			return errors.New("landmark has no permitted direction")
		}
		node := dirs[0].edge.End
		entry := landmarkEntry{Seed: seed}
		for _, reverse := range []bool{false, true} {
			filename := fmt.Sprintf("landmark-%02d-%t.bin", i, reverse)
			receiptName := filename + ".json"
			var vector landmarkVector
			if b, err := readBoundedFile(filepath.Join(out, receiptName), 1<<20); err == nil {
				if err := json.Unmarshal(b, &vector); err != nil {
					return err
				}
				if vector.File != filename || vector.SeedNode != node || vector.Reverse != reverse || vector.Bytes != vectorBytes {
					return errors.New("foreign landmark vector receipt")
				}
				if err := verifyVectorFile(ctx, filepath.Join(out, filename), vector); err != nil {
					return err
				}
			} else if !os.IsNotExist(err) {
				return err
			} else {
				vector, err = prepareLandmarkVector(ctx, rtr, d, out, filename, node, reverse)
				if err != nil {
					return fmt.Errorf("landmark %s reverse=%v: %w", seed.Name, reverse, err)
				}
				if err := publishJSON(out, receiptName, vector); err != nil {
					return err
				}
			}
			if reverse {
				entry.Reverse = vector
			} else {
				entry.Forward = vector
			}
			runtime.GC()
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	return publishJSON(out, "landmarks.json", manifest)
}
func prepareLandmarkVector(ctx context.Context, s *Router, d *denseNodes, dir, name string, node ID, reverse bool) (landmarkVector, error) {
	s.Reader.recordPageReuse = true
	defer func() { s.Reader.recordPageReuse = false }()
	start := time.Now()
	pin := landmarkVector{File: name, SeedNode: node, Reverse: reverse}
	root, err := d.index(node)
	if err != nil {
		return pin, err
	}
	dist := make([]float64, d.count)
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[root] = 0
	heap := nodeHeap{cost: dist, positions: make([]int32, d.count)}
	heap.push(root)
	relax := func(id ID, cost float64) error {
		to, err := d.index(id)
		if err != nil {
			var missing *MissingTileError
			if errors.As(err, &missing) {
				pin.Missing++
				return nil
			}
			return err
		}
		if cost >= dist[to] {
			return nil
		}
		if heap.positions[to] < 0 {
			return errors.New("settled node improved in nonnegative relaxed graph")
		}
		dist[to] = cost
		heap.push(to)
		return nil
	}
	for len(heap.nodes) > 0 {
		if err := ctx.Err(); err != nil {
			return pin, err
		}
		dense := heap.pop()
		id := d.id(dense)
		n, err := s.Reader.Node(id)
		if err != nil {
			return pin, err
		}
		pin.Settled++
		transitions, err := s.Reader.Transitions(n)
		if err != nil {
			return pin, err
		}
		for _, to := range transitions {
			if err := relax(to, dist[dense]); err != nil {
				return pin, err
			}
		}
		if reverse {
			missing, err := s.incoming(n, true, func(edge Edge, next ID) error {
				allowed, err := s.Allowed(edge)
				if err != nil {
					return err
				}
				if !allowed {
					return nil
				}
				return relax(next, dist[dense]+edge.Length*3.6/float64(edge.Speed))
			})
			pin.Missing += missing
			if err != nil {
				return pin, err
			}
		} else {
			for i := 0; i < n.EdgeCount; i++ {
				edge, err := s.Reader.Edge(id.WithIndex(n.EdgeIndex + i))
				if err != nil {
					return pin, err
				}
				allowed, err := s.Allowed(edge)
				if err != nil {
					return pin, err
				}
				if !allowed {
					continue
				}
				if err := relax(edge.End, dist[dense]+edge.Length*3.6/float64(edge.Speed)); err != nil {
					return pin, err
				}
			}
		}
	}
	size := (64 + int64(d.count)*4 + pageSize - 1) / pageSize * pageSize
	if err := diskReserve(dir, size, 32<<30); err != nil {
		return pin, err
	}
	f, err := os.CreateTemp(dir, ".landmark-*")
	if err != nil {
		return pin, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	hash := sha256.New()
	w := bufio.NewWriterSize(io.MultiWriter(f, hash), 1<<20)
	var header [64]byte
	copy(header[:], landmarkSchema)
	binary.LittleEndian.PutUint32(header[32:], d.count)
	binary.LittleEndian.PutUint64(header[40:], uint64(node))
	if reverse {
		header[48] = 1
	}
	if _, err := w.Write(header[:]); err != nil {
		return pin, err
	}
	for i, value := range dist {
		if i%262144 == 0 {
			if err := ctx.Err(); err != nil {
				return pin, err
			}
			if err := diskReserve(dir, 1<<20, 32<<30); err != nil {
				return pin, err
			}
		}
		q := float32(value)
		if float64(q) > value {
			q = math.Nextafter32(q, float32(math.Inf(-1)))
		}
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(q))
		if _, err := w.Write(b[:]); err != nil {
			return pin, err
		}
	}
	if _, err := w.Write(make([]byte, size-(64+int64(d.count)*4))); err != nil {
		return pin, err
	}
	if err := w.Flush(); err != nil {
		return pin, err
	}
	if err := f.Sync(); err != nil {
		return pin, err
	}
	pin.Bytes = size
	pin.SHA256 = hex.EncodeToString(hash.Sum(nil))
	pin.Seconds = time.Since(start).Seconds()
	pin.CumulativeCache = s.Reader.CacheReport()
	target := filepath.Join(dir, name)
	if err := os.Link(f.Name(), target); err != nil {
		if !os.IsExist(err) {
			return pin, err
		}
		// A crash after linking but before its receipt can leave a complete orphan.
		// Recompute and compare it; never trust or overwrite an unreceipted vector.
		if err := verifyVectorFile(ctx, target, pin); err != nil {
			return pin, err
		}
	}
	return pin, nil
}
func verifyVectorFile(ctx context.Context, path string, pin landmarkVector) error {
	if checkDigest(pin.SHA256) != nil {
		return errors.New("invalid landmark checksum")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() != pin.Bytes {
		return errors.New("landmark vector size mismatch")
	}
	h := sha256.New()
	if _, err := io.Copy(h, contextReader{ctx, f}); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != pin.SHA256 {
		return errors.New("landmark vector checksum mismatch")
	}
	return nil
}
