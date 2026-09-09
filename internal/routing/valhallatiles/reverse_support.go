package valhallatiles

// A source-checked bitset permits the cheap opposing-edge reverse view only at
// nodes where every retained ordinary incoming edge has a reciprocal partner.
// Exceptional nodes use full neighbor enumeration. The certificate is rebuilt
// from graph records; it is never inferred from a successful route sample.
import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const reverseSupportSchema = "scout-reverse-support-v1"

type reverseSupport struct {
	dense *denseNodes
	bits  []byte
}
type reverseSupportReceipt struct {
	Schema              string `json:"schema"`
	Graph               string `json:"graph_receipt_sha256"`
	SHA256              string `json:"sha256"`
	Bytes               int    `json:"bytes"`
	Nodes               uint32 `json:"nodes"`
	Exceptions, Missing uint64
}

func scanReverseSupport(ctx context.Context, r *Reader) (*reverseSupport, reverseSupportReceipt, error) {
	previousReuse := r.recordPageReuse
	r.recordPageReuse = true
	defer func() { r.recordPageReuse = previousReuse }()
	d, err := newDenseNodes(r)
	if err != nil {
		return nil, reverseSupportReceipt{}, err
	}
	support := &reverseSupport{dense: d, bits: make([]byte, (d.count+7)/8)}
	meta := reverseSupportReceipt{Schema: reverseSupportSchema, Nodes: d.count}
	s := &Router{Reader: r}
	for _, id := range r.TileIDs() {
		t, err := r.get(id)
		if err != nil {
			return nil, meta, err
		}
		for i := 0; i < t.nodes; i++ {
			if err := ctx.Err(); err != nil {
				return nil, meta, err
			}
			n, err := r.Node(id.WithIndex(i))
			if err != nil {
				return nil, meta, err
			}
			for j := 0; j < n.EdgeCount; j++ {
				e, err := r.Edge(id.WithIndex(n.EdgeIndex + j))
				if err != nil {
					return nil, meta, err
				}
				if e.Shortcut {
					continue
				}
				if _, ok := r.index[e.End.Base()]; !ok {
					meta.Missing++
					continue
				}
				opp, err := s.opposite(e)
				if err != nil {
					return nil, meta, err
				}
				if opp.End != n.ID {
					return nil, meta, errors.New("opposing endpoint mismatch in reverse certificate")
				}
				if opp.Shortcut || int(opp.OppIndex) != j {
					index, err := d.index(e.End)
					if err != nil {
						return nil, meta, err
					}
					mask := byte(1 << (index % 8))
					if support.bits[index/8]&mask == 0 {
						meta.Exceptions++
						support.bits[index/8] |= mask
					}
				}
			}
		}
	}
	return support, meta, nil
}
func PrepareReverseSupport(ctx context.Context, dir string) error {
	r, err := OpenPreparedScout(ctx, dir, 128<<20)
	if err != nil {
		return err
	}
	defer r.Close()
	r.records = &recordCache{}
	support, meta, err := scanReverseSupport(ctx, r)
	if err != nil {
		return err
	}
	graph, err := readBoundedFile(filepath.Join(dir, "receipt.json"), maxPreparedIndex)
	if err != nil {
		return err
	}
	if hexSum(graph) != r.preparedSHA {
		return errors.New("graph receipt changed during preparation")
	}
	meta.Graph = hexSum(graph)
	data := make([]byte, 64+len(support.bits))
	copy(data, reverseSupportSchema)
	binary.LittleEndian.PutUint32(data[32:], meta.Nodes)
	copy(data[64:], support.bits)
	meta.Bytes = len(data)
	meta.SHA256 = hexSum(data)
	if err := diskReserve(dir, int64(len(data)), 32<<30); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".reverse-support-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := os.Link(f.Name(), filepath.Join(dir, "reverse-support.bin")); err != nil {
		return err
	}
	return publishJSON(dir, "reverse-support.json", meta)
}
func (s *Router) loadReverseSupport(ctx context.Context, dir string) error {
	b, err := readBoundedFile(filepath.Join(dir, "reverse-support.json"), 1<<20)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var meta reverseSupportReceipt
	if err := json.Unmarshal(b, &meta); err != nil {
		return err
	}
	graph, err := readBoundedFile(filepath.Join(dir, "receipt.json"), maxPreparedIndex)
	if err != nil {
		return err
	}
	if meta.Schema != reverseSupportSchema || meta.Graph != hexSum(graph) || meta.Graph != s.Reader.preparedSHA || meta.Nodes > maxLandmarkNodes || meta.Bytes != 64+int((meta.Nodes+7)/8) {
		return errors.New("invalid reverse support receipt")
	}
	d, err := newDenseNodes(s.Reader)
	if err != nil {
		return err
	}
	if d.count != meta.Nodes {
		return errors.New("reverse support node order mismatch")
	}
	data, err := readBoundedFile(filepath.Join(dir, "reverse-support.bin"), 16<<20)
	if err != nil {
		return err
	}
	if len(data) != meta.Bytes || hexSum(data) != meta.SHA256 || string(data[:len(reverseSupportSchema)]) != reverseSupportSchema || u32(data, 32) != d.count {
		return errors.New("reverse support checksum/header mismatch")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.reverseSupport = &reverseSupport{dense: d, bits: data[64:]}
	return nil
}
func (s *reverseSupport) exception(id ID) (bool, error) {
	index, err := s.dense.index(id)
	if err != nil {
		return false, err
	}
	return s.bits[index/8]&(1<<(index%8)) != 0, nil
}
