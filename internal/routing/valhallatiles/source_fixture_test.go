package valhallatiles

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Source tests use the supported immutable preparation pipeline in a disposable
// directory. Adversarial byte mutations reopen only that test-owned tile file.
func prepareSourceFixture(t *testing.T, root string, lock ScoutLock, cache int64) *Reader {
	t.Helper()
	out := filepath.Join(t.TempDir(), "prepared")
	budget := ScoutBudgets{CompressedBytes: 1 << 30, ExpandedBytes: 1 << 30, ReserveBytes: 32 << 30}
	if e := diskReserve(filepath.Dir(out), 1<<20, budget.ReserveBytes); e != nil {
		t.Skip(e)
	}
	if e := PrepareScoutPackages(context.Background(), root, out, lock, budget); e != nil {
		t.Fatal(e)
	}
	r, e := OpenPreparedScout(context.Background(), out, cache)
	if e != nil {
		t.Fatal(e)
	}
	r.f.Close()
	r.f, e = os.OpenFile(filepath.Join(out, "tiles.bin"), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { r.Close() })
	return r
}
