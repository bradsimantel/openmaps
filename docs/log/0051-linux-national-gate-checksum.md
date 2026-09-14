# Linux national gate checksum

Date: 2026-09-14

Scope: qualification follow-up to revision `e36f186cf96f4e5bd698d403286599024a528a42`

Two independent qualification builds on the intended Linux/amd64 national
build host produced the same retained-data digest:

`3b4f68333e1508d33f1c4610dfc630e5735871a0f6f0b5475e95a7462c56c359`

The prior checked-in digest came from the retained macOS/arm64 qualification
artifact. A row-level comparison found the same source rows and the same output
row counts. Relationships, attribute provenance, and rejections were identical.
Exactly 14 transportation entities, and their corresponding 14 source records,
differed only in one representative latitude or longitude by one binary64 ULP.
Those coordinates are calculated by the spherical-length midpoint operation.
The exact retained-data digest therefore was not portable between the two CPU
and operating-system targets even though the material data was equivalent.

The gate now pins the twice-reproduced Linux/amd64 digest because Linux/amd64 is
the intended national build target. This records the observed platform boundary;
it does not establish cross-platform bitwise determinism for calculated
coordinates. A future change to the midpoint calculation or target platform must
run and qualify the gate deliberately rather than updating this value from a
single unexplained mismatch.
