package routing

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

const DefaultConstructionSortRecords = 1 << 20
const MaxConstructionSortRecords = 4 << 20
const constructionMergeFanIn = 16
const constructionStreamBytes = 64 << 10

type constructionScratch struct {
	directory string
	records   int
}
type constructionScratchKey struct{}

// WithConstructionScratch sets the directory and bounded sort batch for offline
// segment/guard identity validation. It does not bound the graph, parser, or
// hierarchy. Scratch files are private, ephemeral, and removed on return.
func WithConstructionScratch(ctx context.Context, directory string, records int) (context.Context, error) {
	if records < 1 || records > MaxConstructionSortRecords {
		return nil, fmt.Errorf("construction sort records must be 1..%d", MaxConstructionSortRecords)
	}
	return context.WithValue(ctx, constructionScratchKey{}, constructionScratch{directory, records}), nil
}

// Only ordinals are sorted or written. Keys remain in the already resident
// authoritative records; no copied strings or graph-sized validation map exists.
// The final unique, sorted sequence has exactly one ordinal for every input.
// It also resolves restriction references without a resident lookup map.
type constructionIDs struct {
	segments []Segment
	guards   []Guard
	values   []uint64
	file     *os.File
	dir      string
	count    int
}

func (x *constructionIDs) key(v uint64) (string, error) {
	if v >= uint64(x.count) {
		return "", fmt.Errorf("construction identity ordinal out of range")
	}
	if v < uint64(len(x.segments)) {
		return x.segments[v].ID, nil
	}
	return x.guards[v-uint64(len(x.segments))].Segment, nil
}
func (x *constructionIDs) compare(a, b uint64) int {
	ka, _ := x.key(a)
	kb, _ := x.key(b)
	if ka < kb {
		return -1
	}
	if ka > kb {
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
func (x *constructionIDs) close() error {
	x.values = nil
	var err error
	if x.file != nil {
		err = x.file.Close()
		x.file = nil
	}
	if x.dir != "" {
		if e := os.RemoveAll(x.dir); err == nil {
			err = e
		}
		x.dir = ""
	}
	return err
}
func (x *constructionIDs) run(pass, run int) string {
	return filepath.Join(x.dir, fmt.Sprintf("%d-%d", pass, run))
}

func newConstructionIDs(ctx context.Context, segments []Segment, guards []Guard) (_ *constructionIDs, err error) {
	options, ok := ctx.Value(constructionScratchKey{}).(constructionScratch)
	if !ok {
		options.records = DefaultConstructionSortRecords
	}
	// These match the graph's capacity checks and keep ordinal arithmetic safe on
	// supported 64-bit construction hosts, including temporary file extents.
	if uint64(len(segments))+uint64(len(guards)) > uint64(^uint(0)>>1)/8 {
		return nil, fmt.Errorf("construction identity capacity exceeded")
	}
	x := &constructionIDs{segments: segments, guards: guards, count: len(segments) + len(guards)}
	defer func() {
		if err != nil {
			err = errors.Join(err, x.close())
		}
	}()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	x.values = make([]uint64, min(x.count, options.records))
	if x.count > options.records {
		x.dir, err = os.MkdirTemp(options.directory, ".routing-ids-*")
		if err != nil {
			return nil, err
		}
	}
	runs := 0
	for lo := 0; lo < x.count; {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		n := min(options.records, x.count-lo)
		batch := x.values[:n]
		for i := range batch {
			batch[i] = uint64(lo + i)
		}
		// Cancellation is checked before/after each bounded in-memory sort.
		slices.SortFunc(batch, x.compare)
		if err = x.checkIDs(ctx, batch); err != nil {
			return nil, err
		}
		if x.dir != "" {
			if err = writeIdentityRun(ctx, x.run(0, runs), batch); err != nil {
				return nil, err
			}
		}
		lo += n
		runs++
	}
	if x.dir == "" {
		return x, nil
	}
	// Reuse only a fixed fan-in of readers. Run names come from counters, so a
	// smaller batch never creates an unbounded in-memory list of run metadata.
	x.values = nil
	pass := 0
	for runs > 1 {
		next := 0
		for lo := 0; lo < runs; lo += constructionMergeFanIn {
			if err = x.merge(ctx, pass, lo, min(runs, lo+constructionMergeFanIn), x.run(pass+1, next)); err != nil {
				return nil, err
			}
			next++
		}
		pass++
		runs = next
	}
	path := x.run(pass, 0)
	if err = os.Chmod(path, 0400); err != nil {
		return nil, err
	}
	if err = x.openRun(path); err != nil {
		return nil, err
	}
	return x, nil
}

func (x *constructionIDs) openRun(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err == nil && info.Size() != int64(x.count)*8 {
		err = fmt.Errorf("construction identity record count mismatch")
	}
	if err != nil {
		return errors.Join(err, f.Close())
	}
	x.file = f
	return nil
}

func (x *constructionIDs) checkIDs(ctx context.Context, values []uint64) error {
	previous := ""
	for i, v := range values {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		key, err := x.key(v)
		if err != nil {
			return err
		}
		if key == "" || i > 0 && key <= previous {
			return fmt.Errorf("empty, duplicate or unordered routing segment/guard identity")
		}
		previous = key
	}
	return nil
}

func writeIdentityRun(ctx context.Context, path string, values []uint64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, constructionStreamBytes)
	var raw [8]byte
	for i, v := range values {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		binary.LittleEndian.PutUint64(raw[:], v)
		if _, err := w.Write(raw[:]); err != nil {
			return err
		}
	}
	if err = w.Flush(); err != nil {
		return err
	}
	return f.Close()
}

type identityRun struct {
	f         *os.File
	r         *bufio.Reader
	value     uint64
	key       string
	remaining int64
	raw       [8]byte
}

func (r *identityRun) advance(x *constructionIDs) error {
	if _, err := io.ReadFull(r.r, r.raw[:]); err != nil {
		return fmt.Errorf("truncated construction identity run: %w", err)
	}
	v := binary.LittleEndian.Uint64(r.raw[:])
	key, err := x.key(v)
	if err != nil {
		return err
	}
	if key == "" || r.key != "" && key <= r.key {
		return fmt.Errorf("duplicate or unordered construction identity run")
	}
	r.value, r.key = v, key
	r.remaining--
	return nil
}

func (x *constructionIDs) merge(ctx context.Context, pass, lo, hi int, output string) error {
	readers := make([]*identityRun, 0, constructionMergeFanIn)
	defer func() {
		for _, r := range readers {
			r.f.Close()
		}
	}()
	for i := lo; i < hi; i++ {
		f, err := os.Open(x.run(pass, i))
		if err != nil {
			return err
		}
		r := &identityRun{f: f, r: bufio.NewReaderSize(f, constructionStreamBytes)}
		readers = append(readers, r)
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if info.Size() == 0 || info.Size()%8 != 0 || info.Size()/8 > int64(x.count) {
			return fmt.Errorf("invalid construction identity run size")
		}
		r.remaining = info.Size() / 8
		if err = r.advance(x); err != nil {
			return err
		}
	}
	// The separate fixed-size heap can shrink without dropping file ownership.
	heap := append([]*identityRun(nil), readers...)
	slices.SortFunc(heap, func(a, b *identityRun) int { return x.compare(a.value, b.value) })
	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, constructionStreamBytes)
	var raw [8]byte
	previous := ""
	for count := 0; len(heap) > 0; count++ {
		if count%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		r := heap[0]
		if r.key <= previous {
			return fmt.Errorf("duplicate routing segment/guard identity")
		}
		previous = r.key
		binary.LittleEndian.PutUint64(raw[:], r.value)
		if _, err = w.Write(raw[:]); err != nil {
			return err
		}
		if r.remaining == 0 {
			heap[0] = heap[len(heap)-1]
			heap = heap[:len(heap)-1]
		} else if err = r.advance(x); err != nil {
			return err
		}
		for i := 0; 2*i+1 < len(heap); {
			j := 2*i + 1
			if j+1 < len(heap) && heap[j+1].key < heap[j].key {
				j++
			}
			if heap[i].key <= heap[j].key {
				break
			}
			heap[i], heap[j] = heap[j], heap[i]
			i = j
		}
	}
	if err = w.Flush(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	for _, r := range readers {
		if err = r.f.Close(); err != nil {
			return err
		}
		if err = os.Remove(r.f.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (x *constructionIDs) at(i int) (uint64, error) {
	if i < 0 || i >= x.count {
		return 0, fmt.Errorf("construction identity index out of range")
	}
	if x.file == nil {
		return x.values[i], nil
	}
	var raw [8]byte
	if _, err := x.file.ReadAt(raw[:], int64(i)*8); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint64(raw[:])
	_, err := x.key(v)
	return v, err
}

func (x *constructionIDs) segment(id string) (int, bool, error) {
	lo, hi := 0, x.count
	for lo < hi {
		mid := lo + (hi-lo)/2
		v, err := x.at(mid)
		if err != nil {
			return 0, false, err
		}
		key, err := x.key(v)
		if err != nil {
			return 0, false, err
		}
		if key == id {
			return int(v), v < uint64(len(x.segments)), nil
		}
		if key < id {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return 0, false, nil
}
