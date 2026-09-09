# Synthetic nested packages

These 1,032 compressed bytes are deterministic, hand-encoded regression inputs,
not OSM Scout data or provider compatibility evidence. `lock.json` pins every
package and tile. Tests read them offline; they never acquire data.

The graph is the `fixture(t, true, true)` graph in `reader_test.go`: A–B–C–D
with a B–E–C detour, split at longitude 8.75°. Package 1 holds A's tile and
package 2 holds the remaining nodes. The A→B incoming mask prohibits B→C;
the complex record prohibits A→B→C→D. Source ways are synthetic 100–104.

Construction used those fixture tar members, changed the header version to
3.4.0, and named them with canonical Scout tile paths. Each package contains its
tile compressed with gzip (mtime 0), a matching `.tar.list`, and a timestamp
containing `synthetic\n`. USTAR headers use mode 0600 and timestamp 0; the outer
tar is bzip2-compressed. All IDs, lengths, geometry, restrictions and dataset 0
are synthetic. The compatibility test compares the decoded route with the
independently constructed ordinary fixture, including geometry and cost.
