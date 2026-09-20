# National viewport relevance gate

**Date:** 2026-09-20

**Base revision:** `e14956360cdf285bbef014eee55fdf2e5963427f`

**Lookup generation:** `/srv/openmaps/data/national-catalog-research-2cfd7fc`

**Manifest SHA-256:**
`7252165542a53daf0bda1a34e30fb9dd3ae6966015aeda9c20777017e092ecec`

## Purpose and scoring decision

The maintained 99-case national relevance set contains no viewport and remains
the comparable measure of un-biased autocomplete. Viewport work therefore did
not change its latest recorded 71/99 result. This change adds a separate
six-case dataset at `config/us-viewport-query-checks.json`; passing it is
reported as 6/6 rather than inflating the core denominator to 105.

The six cases cover normal and very small Chicago and New York `ups store`
viewports plus ambiguous `Main Street` searches in New York and Los Angeles.
The two tiny rectangles contain no matching store. They require all five
results to remain outside the rectangle and be ordered by distance from its
center, baking in soft-bias behavior instead of a hard restriction or fallback
to unrelated national defaults.

## Harness changes

`importer.QueryCheck` now supports:

- `location_bias` as explicit WGS84 `south`, `west`, `north`, `east` fields;
- `min_results`;
- `first_inside_bias`;
- `outside_result_required` and `all_results_outside_bias`; and
- `distance_ordered_from_bias_center`.

The strict loader validates rectangle coordinates and contradictory assertions.
It permits repeated input text only when the viewport differs. Generation
comparison invokes `AutocompleteWithBias` for biased cases and materializes
result coordinates before checking containment and distance. The opt-in
`TestNationalViewportQualification` evaluates the same assertions directly
against one immutable national artifact.

Forward-geocoding bounds are not part of this dataset because geocoding has
different exact-candidate and response semantics. They retain their existing
deterministic API and store tests.

## National result

The exact deployed generation passed **6/6**:

| Case | First result | Distance from center | Time |
| --- | --- | ---: | ---: |
| Chicago `ups store`, normal viewport | `om_6645d46a5a73b0f7ffb3054ff51e69b7` | 5,181 m | 2.91s |
| Chicago `ups store`, tiny empty viewport | `om_9361e989091c6520a78d581d98171a8c` | 523 m | 2.73s |
| New York `ups store`, normal viewport | `om_08e6ade4c2f09c810e6fae5eb610cb68` | 2,011 m | 4.30s |
| New York `ups store`, tiny empty viewport | `om_3924d84f69180ff7826a4ebecdea8572` | 182 m | 4.31s |
| New York `Main Street` | `om_0121d6b5e3c88873ac64519b3ecd3186` | 22,814 m | 19.06s |
| Los Angeles `Main Street` | `om_0bb597d681ad0730d11806b288b19d5f` | 6,125 m | 29.15s |

The full viewport run took 64.60s. The normal deterministic suite, race tests
for the changed packages, `go vet ./...`, JavaScript syntax checks and
`git diff --check` also passed.

## Remaining limitations

- The slice binds current expected first IDs and locations to the reviewed
  national source snapshot. A legitimate source update must be reviewed rather
  than mechanically rewriting expectations.
- Distance ordering is checked only for the selected equal-text-relevance chain
  cases. The public ranking contract still places text relevance ahead of
  distance.
- Six cases protect the reported regressions but do not establish nationwide
  spatial recall. Sparse areas without nearby address context and the bounded
  contextual candidate pool remain documented limitations.
