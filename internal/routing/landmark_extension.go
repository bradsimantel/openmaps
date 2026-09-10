package routing

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

// The proof deliberately rejects every added connection touching any node with
// a finite old landmark distance in either direction. With unchanged old tile
// payloads, the old finite components and their distances are then unchanged;
// all added vertices can only have infinite distances to/from these landmarks.
type landmarkExtensionProof struct {
	Schema                                         string `json:"schema"`
	OldGraph                                       string `json:"old_graph"`
	NewGraph                                       string `json:"new_graph"`
	OldLandmarks                                   string `json:"old_landmarks"`
	OldNodes, NewNodes                             uint32
	AddedTiles, BoundaryEdges, BoundaryTransitions uint64
}

func ReindexLandmarks(ctx context.Context, base, built, dir, out string) error {
	old, err := OpenPreparedRouter(ctx, base, 64<<20)
	if err != nil {
		return err
	}
	defer old.Reader.Close()
	if err := old.EnableLandmarks(ctx, base, built); err != nil {
		return err
	}
	next, err := OpenPreparedRouter(ctx, dir, 64<<20)
	if err != nil {
		return err
	}
	defer next.Reader.Close()
	priorRaw, err := readBoundedFile(filepath.Join(base, "receipt.json"), maxPreparedIndex)
	if err != nil {
		return err
	}
	nextRaw, err := readBoundedFile(filepath.Join(dir, "receipt.json"), maxPreparedIndex)
	if err != nil {
		return err
	}
	if hexSum(priorRaw) != old.Reader.preparedSHA || hexSum(nextRaw) != next.Reader.preparedSHA {
		return errors.New("graph receipt changed during reindex load")
	}
	var prior, expanded preparedScout
	if err := json.Unmarshal(priorRaw, &prior); err != nil {
		return err
	}
	if err := json.Unmarshal(nextRaw, &expanded); err != nil {
		return err
	}
	if prior.Version != expanded.Version || prior.Dataset != expanded.Dataset || prior.Timestamp != expanded.Timestamp {
		return errors.New("mixed landmark extension generation")
	}
	pins := map[ID]preparedTile{}
	for _, p := range expanded.Tiles {
		pins[p.ID] = p
	}
	buffer := make([]byte, 1<<20)
	for _, p := range prior.Tiles {
		q, ok := pins[p.ID]
		if !ok || q.Bytes != p.Bytes || q.SHA256 != p.SHA256 {
			return errors.New("extension removed or changed an old tile")
		}
		// Pins alone do not prove unchanged payloads if a receipt was assembled
		// incorrectly. Check each retained block against both owned files.
		for _, source := range []struct {
			file *os.File
			pin  preparedTile
		}{{old.Reader.f, p}, {next.Reader.f, q}} {
			h := sha256.New()
			if _, err := io.CopyBuffer(h, contextReader{ctx, io.NewSectionReader(source.file, source.pin.Offset, source.pin.Bytes)}, buffer); err != nil {
				return err
			}
			if hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
				return errors.New("retained tile payload differs from its pin")
			}
		}
	}
	raw, err := readBoundedFile(filepath.Join(built, "landmarks.json"), 1<<20)
	if err != nil {
		return err
	}
	if hexSum(raw) != old.landmarks.fingerprint {
		return errors.New("landmark manifest changed during load")
	}
	var manifest landmarkManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	for _, entry := range manifest.Entries {
		if entry.Forward.Missing != 0 || entry.Reverse.Missing != 0 {
			return errors.New("only closed landmark components can be reindexed")
		}
	}
	dense, err := newDenseNodes(next.Reader)
	if err != nil {
		return err
	}
	proof := landmarkExtensionProof{Schema: "openmaps-scout-landmark-extension-v1", OldGraph: old.Reader.preparedSHA, NewGraph: next.Reader.preparedSHA, OldLandmarks: old.landmarks.fingerprint, OldNodes: old.landmarks.dense.count, NewNodes: dense.count}
	for _, id := range next.Reader.TileIDs() {
		if _, ok := old.Reader.index[id]; !ok {
			proof.AddedTiles++
		}
	}
	finiteBoundary := func(id ID) error {
		values, err := old.landmarks.values(id)
		if err != nil {
			return err
		}
		for i := range old.landmarks.vectors {
			if values[i][0] != 0x7f800000 || values[i][1] != 0x7f800000 {
				return fmt.Errorf("new connection touches finite landmark component at %s; recomputation required", id)
			}
		}
		return nil
	}
	next.Reader.recordPageReuse = true
	for _, id := range next.Reader.TileIDs() {
		tile, err := next.Reader.get(id)
		if err != nil {
			return err
		}
		_, fromOld := old.Reader.index[id]
		for i := 0; i < tile.nodes; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := next.Reader.Node(id.WithIndex(i))
			if err != nil {
				return err
			}
			transitions, err := next.Reader.Transitions(n)
			if err != nil {
				return err
			}
			for _, to := range transitions {
				if _, retained := next.Reader.index[to.Base()]; !retained {
					continue
				}
				_, toOld := old.Reader.index[to.Base()]
				if fromOld == toOld {
					continue
				}
				proof.BoundaryTransitions++
				check := to
				if fromOld {
					check = n.ID
				}
				if err := finiteBoundary(check); err != nil {
					return err
				}
			}
			for j := 0; j < n.EdgeCount; j++ {
				e, err := next.Reader.Edge(id.WithIndex(n.EdgeIndex + j))
				if err != nil {
					return err
				}
				if _, retained := next.Reader.index[e.End.Base()]; !retained {
					continue
				}
				_, toOld := old.Reader.index[e.End.Base()]
				if fromOld == toOld {
					continue
				}
				ok, err := next.Allowed(e)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
				proof.BoundaryEdges++
				check := e.End
				if fromOld {
					check = n.ID
				}
				if err := finiteBoundary(check); err != nil {
					return err
				}
			}
		}
	}
	next.Reader.recordPageReuse = false
	if err := os.Mkdir(out, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	if err := publishOrMatchJSON(out, "extension.json", proof); err != nil {
		return err
	}
	proofRaw, err := readBoundedFile(filepath.Join(out, "extension.json"), 65536)
	if err != nil {
		return err
	}
	manifest.GraphReceiptSHA256 = next.Reader.preparedSHA
	manifest.NodeCount = dense.count
	manifest.ReindexedFrom = proof.OldLandmarks
	manifest.ExtensionSHA256 = hexSum(proofRaw)
	bytesPer := (64 + int64(dense.count)*4 + pageSize - 1) / pageSize * pageSize
	var pending int64
	for _, e := range manifest.Entries {
		for _, p := range []landmarkVector{e.Forward, e.Reverse} {
			if _, err := os.Stat(filepath.Join(out, p.File+".json")); os.IsNotExist(err) {
				pending += bytesPer
			} else if err != nil {
				return err
			}
		}
	}
	if err := diskReserve(out, pending, 32<<30); err != nil {
		return err
	}
	for i := range manifest.Entries {
		for j := 0; j < 2; j++ {
			source := manifest.Entries[i].Forward
			if j == 1 {
				source = manifest.Entries[i].Reverse
			}
			pin, err := reindexVector(ctx, old.landmarks.vectors[i][j].reader.f, old.landmarks.dense, dense, out, source)
			if err != nil {
				return err
			}
			if j == 0 {
				manifest.Entries[i].Forward = pin
			} else {
				manifest.Entries[i].Reverse = pin
			}
		}
	}
	return publishOrMatchJSON(out, "landmarks.json", manifest)
}

func publishOrMatchJSON(dir, name string, value any) error {
	path := filepath.Join(dir, name)
	b, err := readBoundedFile(path, 1<<20)
	if os.IsNotExist(err) {
		return publishJSON(dir, name, value)
	}
	if err != nil {
		return err
	}
	want, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var a, c any
	if json.Unmarshal(b, &a) != nil || json.Unmarshal(want, &c) != nil || !reflect.DeepEqual(a, c) {
		return errors.New("existing publication has different reindex inputs")
	}
	return nil
}

func reindexVector(ctx context.Context, source *os.File, old, next *denseNodes, out string, prior landmarkVector) (landmarkVector, error) {
	started := time.Now()
	pin := landmarkVector{File: prior.File, SeedNode: prior.SeedNode, Reverse: prior.Reverse, Settled: prior.Settled, Bytes: (64 + int64(next.count)*4 + pageSize - 1) / pageSize * pageSize}
	if b, err := readBoundedFile(filepath.Join(out, pin.File+".json"), 1<<20); err == nil {
		var saved landmarkVector
		if err := json.Unmarshal(b, &saved); err != nil {
			return pin, err
		}
		if saved.File != pin.File || saved.SeedNode != pin.SeedNode || saved.Reverse != pin.Reverse || saved.Settled != pin.Settled || saved.Bytes != pin.Bytes || saved.Missing != 0 {
			return pin, errors.New("foreign reindexed vector receipt")
		}
		return saved, verifyVectorFile(ctx, filepath.Join(out, pin.File), saved)
	} else if !os.IsNotExist(err) {
		return pin, err
	}
	f, err := os.CreateTemp(out, ".reindexed-*")
	if err != nil {
		return pin, err
	}
	defer f.Close()
	defer os.Remove(f.Name())
	hash := sha256.New()
	writer := io.MultiWriter(f, hash)
	var header [64]byte
	copy(header[:], landmarkSchema)
	binary.LittleEndian.PutUint32(header[32:], next.count)
	binary.LittleEndian.PutUint64(header[40:], uint64(pin.SeedNode))
	if pin.Reverse {
		header[48] = 1
	}
	if _, err := writer.Write(header[:]); err != nil {
		return pin, err
	}
	buf := make([]byte, 1<<20)
	for _, entry := range next.tiles {
		before, exists := old.byTile[entry.ID]
		if exists && before.Count != entry.Count {
			return pin, errors.New("changed old node count")
		}
		for at := int64(0); at < int64(entry.Count)*4; {
			if err := ctx.Err(); err != nil {
				return pin, err
			}
			n := min(int64(len(buf)), int64(entry.Count)*4-at)
			if err := diskReserve(out, n, 32<<30); err != nil {
				return pin, err
			}
			if exists {
				if _, err := source.ReadAt(buf[:n], 64+int64(before.First)*4+at); err != nil {
					return pin, err
				}
			} else {
				for k := 0; k < int(n); k += 4 {
					binary.LittleEndian.PutUint32(buf[k:k+4], 0x7f800000)
				}
			}
			if _, err := writer.Write(buf[:n]); err != nil {
				return pin, err
			}
			at += n
		}
	}
	if _, err := writer.Write(make([]byte, pin.Bytes-(64+int64(next.count)*4))); err != nil {
		return pin, err
	}
	if err := f.Sync(); err != nil {
		return pin, err
	}
	pin.SHA256 = hex.EncodeToString(hash.Sum(nil))
	pin.Seconds = time.Since(started).Seconds()
	if err := os.Link(f.Name(), filepath.Join(out, pin.File)); err != nil {
		if !os.IsExist(err) {
			return pin, err
		}
		if err := verifyVectorFile(ctx, filepath.Join(out, pin.File), pin); err != nil {
			return pin, err
		}
	}
	if err := publishJSON(out, pin.File+".json", pin); err != nil {
		return pin, err
	}
	return pin, nil
}
