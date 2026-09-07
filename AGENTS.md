# Working on Open Maps

## Project context

Read `README.md` for the product scope and intended architecture. Open Maps is a
Google Maps API replacement built with Go and SQLite, with MapLibre GL JS and
Protomaps for map display. The current focus is places, geocoding, and routing.

Keep documentation clear about what is implemented, what is planned, and what
remains undecided. Do not present a proposal as an established project decision.

## How we work

- Carry authorized work through implementation and appropriate verification.
  Make routine, reversible decisions without repeatedly asking for permission.
- Ask for clarification when a material ambiguity cannot be resolved from the
  request or project context. Continue independent work while awaiting an answer.
- Keep changes focused on the requested outcome. Avoid unrelated refactors,
  speculative abstractions, and dependencies without a concrete need.
- Explain meaningful decisions and tradeoffs in plain language. Report what
  changed, how it was checked, and any unresolved limitations.
- Preserve existing user work. Do not overwrite unrelated changes.

## Code organization

- Prefer explicit domain names: `places`, `geocoding`, `routing`, and `api`.
  Avoid broad packages such as `geo`, `search`, `common`, or `utils` when they hide
  the actual responsibility.
- Keep Google request/response translation in `api`. Internal operations should
  use types appropriate to the domain rather than depend on Google wire formats.
- Places autocomplete may return businesses, addresses, streets, and geographic
  areas. Do not restrict it to businesses because of the package name.
- Places and geocoding may share SQLite records and indexes. Start with queries
  in the package that owns the operation; extract shared code when actual usage
  makes its responsibility clear.
- Keep routing independent of text search and address resolution. The API layer
  coordinates resolution before routing; routing owns road snapping.
- Start with one Go service. Introduce service boundaries or additional runtime
  infrastructure only for a demonstrated requirement.
- Prefer straightforward Go and SQL. Introduce interfaces at useful boundaries
  rather than creating one for every type.

## API and data behavior

- Before implementing a compatibility endpoint, establish the Google API version
  and supported parameters, fields, and error behavior. Consult current official
  documentation when verifying an external contract.
- Make unsupported behavior explicit. Do not silently ignore a parameter that
  would materially change the result or fabricate unavailable data.
- Preserve entity types and relationships. A business, address, and building
  must not be treated as interchangeable identities.
- Keep public IDs stable across imports and independent of transient row IDs.
- Retain data provenance, source IDs, release versions, and applicable
  attribution information. Make imports reproducible.
- Be explicit about coordinate order, distance units, and match precision at
  boundaries. Distinguish address matches from street or locality fallbacks.
- Keep basemap tiles separate from the source data used for lookup and routing.

## Verification and documentation

- Test meaningful behavior: API contracts and errors, geographic edge cases,
  search relevance, and routing restrictions. Avoid tests that merely repeat the
  implementation.
- Use small, deterministic fixtures for routine tests. Keep large datasets and
  network-dependent checks separate from the default test suite.
- Format changed Go files with `gofmt`. Once a Go module exists, run
  `go test ./...` and `go vet ./...` for Go changes, and report any checks that
  could not run.
- Check imports for referential integrity and stable identifiers across rebuilds.
  Use representative queries and routes to evaluate data quality.
- Update documentation when behavior, architecture, or development commands
  change. Do not invent setup instructions for tooling that does not exist yet.
- Documentation-only changes need a content and formatting review; they do not
  require unrelated code tests.
