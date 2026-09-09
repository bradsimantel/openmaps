package valhallatiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PublishLandmarkPrefix makes a separate immutable snapshot from completed
// landmark pairs while a larger resumable build continues. Published payloads
// are hardlinked, never changed; the original build manifest is not modified.
func PublishLandmarkPrefix(ctx context.Context, dir, built, out string, count int) error {
	if count < 1 || count > maxLandmarks {
		return errors.New("prefix requires 1..16 completed pairs")
	}
	b, err := readBoundedFile(filepath.Join(built, "build.json"), 1<<20)
	if err != nil {
		return err
	}
	var config struct {
		Schema, Graph, Profile string
		Nodes                  uint32
		Seeds                  []LandmarkSeed
	}
	if err := json.Unmarshal(b, &config); err != nil {
		return err
	}
	if config.Schema != landmarkSchema || config.Profile != CandidateProfile || count > len(config.Seeds) {
		return errors.New("foreign or incomplete landmark build")
	}
	r, err := OpenPreparedScout(ctx, dir, pageSize)
	if err != nil {
		return err
	}
	defer r.Close()
	d, err := newDenseNodes(r)
	if err != nil {
		return err
	}
	if config.Graph != r.preparedSHA || config.Nodes != d.count {
		return errors.New("landmark prefix source mismatch")
	}
	manifest := landmarkManifest{Schema: landmarkSchema, GraphReceiptSHA256: config.Graph, Profile: config.Profile, NodeCount: d.count}
	// Validate all inputs before creating a publication directory.
	for i := 0; i < count; i++ {
		entry := landmarkEntry{Seed: config.Seeds[i]}
		for _, reverse := range []bool{false, true} {
			name := fmt.Sprintf("landmark-%02d-%t.bin", i, reverse)
			b, err := readBoundedFile(filepath.Join(built, name+".json"), 1<<20)
			if err != nil {
				return err
			}
			var pin landmarkVector
			if err := json.Unmarshal(b, &pin); err != nil {
				return err
			}
			if pin.File != name || pin.Reverse != reverse || pin.Bytes != (64+int64(d.count)*4+pageSize-1)/pageSize*pageSize {
				return errors.New("invalid completed vector receipt")
			}
			if _, err := d.index(pin.SeedNode); err != nil {
				return err
			}
			path := filepath.Join(built, name)
			st, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if !st.Mode().IsRegular() {
				return errors.New("prefix input must be a regular immutable file")
			}
			if err := verifyVectorFile(ctx, path, pin); err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			header := make([]byte, 64)
			_, err = f.ReadAt(header, 0)
			f.Close()
			if err != nil {
				return err
			}
			flag := byte(0)
			if reverse {
				flag = 1
			}
			if string(header[:len(landmarkSchema)]) != landmarkSchema || u32(header, 32) != d.count || ID(u64(header, 40)) != pin.SeedNode || header[48] != flag {
				return errors.New("completed vector header mismatch")
			}
			if reverse {
				entry.Reverse = pin
			} else {
				entry.Forward = pin
			}
		}
		if entry.Forward.SeedNode != entry.Reverse.SeedNode {
			return errors.New("prefix directions use different source seeds")
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	if err := os.Mkdir(out, 0700); err != nil {
		return err
	}
	if err := diskReserve(out, 1<<20, 32<<30); err != nil {
		return err
	}
	for _, entry := range manifest.Entries {
		for _, pin := range []landmarkVector{entry.Forward, entry.Reverse} {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := os.Link(filepath.Join(built, pin.File), filepath.Join(out, pin.File)); err != nil {
				return err
			}
		}
	}
	return publishJSON(out, "landmarks.json", manifest)
}
