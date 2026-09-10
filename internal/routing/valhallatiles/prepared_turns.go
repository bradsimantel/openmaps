package valhallatiles

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Offline trie scratch has explicit record caps, independently of graph size.
// Runtime accesses flat sorted transitions through a 4 MiB page cache.
const maxTurnPrefixes = 2000000
const maxTurnRules = 250000

type turnsReceipt struct {
	Schema                            string `json:"schema"`
	GraphReceiptSHA256                string `json:"graph_receipt_sha256"`
	SHA256                            string `json:"sha256"`
	Bytes                             int64  `json:"bytes"`
	States, Transitions, Rules, Timed int
	ZeroAccessMaskHasNoRules          bool
}
type preparedTurns struct {
	t                   *tile
	states, transitions int
}

func PrepareScoutTurns(ctx context.Context, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "routing.json")); err == nil {
		return errors.New("routing publication already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	r, err := OpenPreparedScout(ctx, dir, 32<<20)
	if err != nil {
		return err
	}
	defer r.Close()
	s, err := buildRouter(ctx, r, maxTurnRules, maxTurnPrefixes)
	if err != nil {
		return err
	}
	zeroAccessSafe := true
	for _, id := range r.TileIDs() {
		t, err := r.get(id)
		if err != nil {
			return err
		}
		previousEdge := -1
		for i := 0; i < t.access; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			b, err := t.span(t.accessStart+i*16, 16)
			if err != nil {
				return err
			}
			edgeIndex := field(u64(b, 0), 0, 22)
			if edgeIndex < previousEdge {
				return errors.New("unsorted access restriction records")
			}
			previousEdge = edgeIndex
			edge, err := r.Edge(id.WithIndex(edgeIndex))
			if err != nil {
				return err
			}
			if edge.AccessRestriction == 0 {
				zeroAccessSafe = false
			}
		}
	}
	depths := make([]uint8, len(s.prefixes))
	counts := 0
	for state, p := range s.prefixes {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, child := range p.next {
			depths[child] = depths[state] + 1
			counts++
		}
	}
	bytesCount := int64(64 + 16*len(s.prefixes) + 16*counts)
	padded := (bytesCount + pageSize - 1) / pageSize * pageSize
	if err := diskReserve(dir, padded, 32<<30); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "turns.bin"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	w := bufio.NewWriterSize(io.MultiWriter(f, hash), 1<<20)
	var header [64]byte
	copy(header[:], "openmaps-scout-turns-v1")
	binary.LittleEndian.PutUint32(header[32:], uint32(len(s.prefixes)))
	binary.LittleEndian.PutUint32(header[36:], uint32(counts))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	first := 0
	for i, p := range s.prefixes {
		var b [16]byte
		binary.LittleEndian.PutUint32(b[:], uint32(first))
		binary.LittleEndian.PutUint32(b[4:], uint32(len(p.next)))
		binary.LittleEndian.PutUint32(b[8:], uint32(p.fail))
		b[12] = depths[i]
		if p.banned {
			b[13] = 1
		}
		if _, err := w.Write(b[:]); err != nil {
			return err
		}
		first += len(p.next)
	}
	for _, p := range s.prefixes {
		if err := ctx.Err(); err != nil {
			return err
		}
		keys := make([]ID, 0, len(p.next))
		for id := range p.next {
			keys = append(keys, id)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, id := range keys {
			var b [16]byte
			binary.LittleEndian.PutUint64(b[:], uint64(id))
			binary.LittleEndian.PutUint32(b[8:], uint32(p.next[id]))
			if _, err := w.Write(b[:]); err != nil {
				return err
			}
		}
	}
	if _, err := w.Write(make([]byte, padded-bytesCount)); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	graph, err := readBoundedFile(filepath.Join(dir, "receipt.json"), maxPreparedIndex)
	if err != nil {
		return err
	}
	if hexSum(graph) != r.preparedSHA {
		return errors.New("graph receipt changed during preparation")
	}
	receipt := turnsReceipt{ZeroAccessMaskHasNoRules: zeroAccessSafe, Schema: "openmaps-scout-routing-v1", GraphReceiptSHA256: hexSum(graph), SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: padded, States: len(s.prefixes), Transitions: counts, Rules: s.RestrictionCount, Timed: s.TimedRestrictionCount}
	return publishJSON(dir, "routing.json", receipt)
}
func hexSum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func publishJSON(dir, name string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".publication-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, werr := f.Write(append(b, '\n'))
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		return err
	}
	if err := os.Link(f.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// OpenPreparedRouter includes graph integrity, a graph-bound turns receipt and
// structural turn validation. Closing its Reader releases both files/caches.
func OpenPreparedRouter(ctx context.Context, dir string, cacheBytes int64) (*Router, error) {
	f, err := os.Open(filepath.Join(dir, "routing.json"))
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	f.Close()
	if err != nil {
		return nil, err
	}
	if len(b) > 65536 {
		return nil, errors.New("routing receipt oversized")
	}
	var pin turnsReceipt
	if err := json.Unmarshal(b, &pin); err != nil {
		return nil, err
	}
	if pin.Schema != "openmaps-scout-routing-v1" || pin.States < 1 || pin.States > maxTurnPrefixes || pin.Transitions != pin.States-1 || pin.Rules < 0 || pin.Rules > maxTurnRules || pin.Timed < 0 || pin.Timed > pin.Rules || checkDigest(pin.SHA256) != nil {
		return nil, errors.New("invalid turns receipt")
	}
	need := int64(64 + 16*pin.States + 16*pin.Transitions)
	if pin.Bytes != (need+pageSize-1)/pageSize*pageSize {
		return nil, errors.New("turns extent mismatch")
	}
	graph, err := readBoundedFile(filepath.Join(dir, "receipt.json"), maxPreparedIndex)
	if err != nil {
		return nil, err
	}
	if len(graph) > maxPreparedIndex || hexSum(graph) != pin.GraphReceiptSHA256 {
		return nil, errors.New("foreign graph receipt")
	}
	r, err := OpenPreparedScout(ctx, dir, cacheBytes)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			r.Close()
		}
	}()
	if r.preparedSHA != pin.GraphReceiptSHA256 {
		return nil, errors.New("graph changed while loading routing snapshot")
	}
	tf, err := os.Open(filepath.Join(dir, "turns.bin"))
	if err != nil {
		return nil, err
	}
	tr := &Reader{f: tf, pages: map[int64]*list.Element{}, limit: 4 << 20}
	r.turnsReader = tr
	st, err := tf.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() != pin.Bytes {
		return nil, errors.New("turns file size mismatch")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, contextReader{ctx, tf}); err != nil {
		return nil, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != pin.SHA256 {
		return nil, errors.New("turns checksum mismatch")
	}
	turns := &preparedTurns{t: &tile{reader: tr, size: int(pin.Bytes)}, states: pin.States, transitions: pin.Transitions}
	header, err := turns.t.span(0, 64)
	if err != nil {
		return nil, err
	}
	if string(bytes.TrimRight(header[:32], "\x00")) != "openmaps-scout-turns-v1" || int(u32(header, 32)) != pin.States || int(u32(header, 36)) != pin.Transitions {
		return nil, errors.New("turns header mismatch")
	}
	first := 0
	for state := 0; state < pin.States; state++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b, err := turns.t.span(64+state*16, 16)
		if err != nil {
			return nil, err
		}
		start, count, fail := int(u32(b, 0)), int(u32(b, 4)), int(u32(b, 8))
		depth := b[12]
		if start != first || count > pin.Transitions-start || fail >= pin.States || depth > 33 || b[13] > 1 || u32(b, 12)>>16 != 0 {
			return nil, errors.New("invalid turn state")
		}
		if state == 0 {
			if depth != 0 || fail != 0 || b[13] != 0 {
				return nil, errors.New("invalid turn root")
			}
		} else {
			fb, err := turns.t.span(64+fail*16, 16)
			if err != nil {
				return nil, err
			}
			if depth == 0 || fb[12] >= depth {
				return nil, errors.New("cyclic turn failure link")
			}
		}
		var previous ID
		for i := start; i < start+count; i++ {
			id, next, err := turns.transition(i)
			if err != nil {
				return nil, err
			}
			if uint64(id) > idMask || id.Level() > 2 || (i > start && id <= previous) || next <= state || next >= pin.States {
				return nil, errors.New("invalid turn transition")
			}
			nb, err := turns.t.span(64+next*16, 16)
			if err != nil {
				return nil, err
			}
			if nb[12] != depth+1 {
				return nil, errors.New("invalid turn prefix depth")
			}
			previous = id
		}
		first += count
	}
	if first != pin.Transitions {
		return nil, errors.New("unindexed turn transitions")
	}
	r.skipEmptyAccessRules = pin.ZeroAccessMaskHasNoRules
	r.records = &recordCache{}
	ok = true
	return &Router{Reader: r, turns: turns, routingSHA: hexSum(b), RestrictionCount: pin.Rules, TimedRestrictionCount: pin.Timed}, nil
}
func (t *preparedTurns) transition(i int) (ID, int, error) {
	b, err := t.t.span(64+t.states*16+i*16, 16)
	if err != nil {
		return 0, 0, err
	}
	if u32(b, 12) != 0 {
		return 0, 0, errors.New("nonzero turn padding")
	}
	return ID(u64(b, 0)), int(u32(b, 8)), nil
}
func (t *preparedTurns) advance(state int, edge ID) (int, bool, error) {
	for steps := 0; steps <= 33; steps++ {
		if state < 0 || state >= t.states {
			return 0, false, errors.New("invalid turn state")
		}
		b, err := t.t.span(64+state*16, 16)
		if err != nil {
			return 0, false, err
		}
		start, count, fail := int(u32(b, 0)), int(u32(b, 4)), int(u32(b, 8))
		lo, hi := 0, count
		for lo < hi {
			mid := (lo + hi) / 2
			id, _, err := t.transition(start + mid)
			if err != nil {
				return 0, false, err
			}
			if id < edge {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo < count {
			id, next, err := t.transition(start + lo)
			if err != nil {
				return 0, false, err
			}
			if id == edge {
				b, err := t.t.span(64+next*16, 16)
				if err != nil {
					return 0, false, err
				}
				return next, b[13] != 0, nil
			}
		}
		if state == 0 {
			return 0, false, nil
		}
		state = fail
	}
	return 0, false, errors.New("turn failure chain exceeded bound")
}
