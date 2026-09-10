package valhallatiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
)

// ScoutLock pins complete package bytes and every uncompressed road tile. Other
// provenance fields in the JSON lock are documentary; these fields are enforced.
type ScoutLock struct {
	Schema        int            `json:"schema"`
	PackageSchema string         `json:"package_schema"`
	Version       string         `json:"tile_version"`
	Dataset       uint64         `json:"dataset_id"`
	Timestamp     string         `json:"timestamp"`
	Packages      []ScoutPackage `json:"packages"`
}
type ScoutPackage struct {
	ID     string      `json:"id"`
	MD5    string      `json:"md5,omitempty"`
	Bytes  int64       `json:"bytes"`
	SHA256 string      `json:"sha256"`
	Tiles  []ScoutTile `json:"tiles"`
}
type ScoutTile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

func checkDigest(digest string) error {
	b, err := hex.DecodeString(digest)
	if err != nil || len(b) != sha256.Size {
		return errors.New("pinned SHA-256 required")
	}
	return nil
}

func scoutID(name string) (ID, error) {
	if !strings.HasPrefix(name, "valhalla/tiles/") || !strings.HasSuffix(name, ".gph.gz") {
		return 0, errors.New("unsupported Scout tile path")
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "valhalla/tiles/"), ".gph.gz"), "/")
	if len(parts) < 2 || len(parts[0]) != 1 || parts[0][0] < '0' || parts[0][0] > '2' {
		return 0, errors.New("only road tile paths supported")
	}
	for _, part := range parts[1:] {
		if len(part) != 3 {
			return 0, errors.New("invalid tile path component")
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, errors.New("invalid tile path digit")
			}
		}
	}
	n, err := strconv.ParseUint(strings.Join(parts[1:], ""), 10, 22)
	if err != nil {
		return 0, err
	}
	level := int(parts[0][0] - '0')
	if n >= uint64([...]int{4050, 64800, 1036800}[level]) {
		return 0, errors.New("tile outside world grid")
	}
	return ID(n<<3) | ID(level), nil
}
