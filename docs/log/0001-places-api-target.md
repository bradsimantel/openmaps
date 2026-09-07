# Places API target for the first milestone

2026-09-07 · Milestone implementation: `1f24467`.

Records the compatibility target and supported subset chosen for the first
milestone. This is a dated decision record, not a maintained API reference.
See the [README](../../README.md) for current setup and supported behavior.

Target: Google Places API (New), REST v1, checked 2026-09-07 against
[Autocomplete reference](https://developers.google.com/maps/documentation/places/web-service/reference/rest/v1/places/autocomplete),
[Details reference](https://developers.google.com/maps/documentation/places/web-service/reference/rest/v1/places/get), and
[Details guide](https://developers.google.com/maps/documentation/places/web-service/place-details).
This is an explicitly limited HTTP compatibility surface, with Open Maps data and
ranking. Google place IDs and the Google Maps JavaScript SDK are not supported.

## Autocomplete

`POST /v1/places:autocomplete`, JSON body:

- `input`: required nonblank string, up to 200 Unicode characters (local limit).
- `languageCode`: omitted, `en`, or `en-US`. Source names are retained; no translation.
- `sessionToken`: optional URL-safe base64 characters, at most 36 ASCII characters.
  Accepted for client compatibility; this service has no billing/session accounting.

Returns up to five `suggestions`, each containing `placePrediction` with `place`
(`places/{id}`), `placeId`, `text.text`, `structuredFormat.mainText.text`, optional
`structuredFormat.secondaryText.text`, and `types`. No query predictions or
fabricated matched ranges. No matches returns `{"suggestions":[]}`.

Optional `X-Goog-FieldMask` (also `fields` or `$fields` query parameter) selects
supported response paths, including parent objects, leaf paths, or `*`. Omitted
means all supported fields. The details field mask rules below also apply here.
All other body parameters are rejected, including location bias/restriction,
region/type filtering, origin, input offsets, query predictions and service-area
options. Results always use the imported launch dataset; there is no IP bias.

## Details

`GET /v1/places/{id}`. A field mask is required, supplied in `X-Goog-FieldMask`,
`fields`, or `$fields`. Exactly one mask source may be used. Masks contain
comma-separated supported paths without whitespace; `*` returns all implemented
fields. Unknown/unsupported fields return an error, including ratings, reviews,
photos, opening hours, viewport and address components.

Supported fields: `id`, `name` (resource name), `displayName` (`text`),
`formattedAddress` when present, `location` (`latitude`, `longitude`), `types`,
`websiteUri` when present, and `attributions` (`provider`, `providerUri`).
Nested paths are supported. Missing source attributes are omitted.
Query parameters `languageCode` and `sessionToken` follow autocomplete rules.
`regionCode` is unsupported because region-dependent formatting is not implemented.
The body must be empty.

Businesses map conservatively to `establishment,point_of_interest`, standalone
addresses to `street_address`, streets to `route`, settlements to `locality,political`,
and administrative areas to `political`. Overture categories are retained in
source records, not asserted to be Google categories.

## Errors and differences

JSON errors use `{"error":{"code":400,"message":"…","status":"INVALID_ARGUMENT"}}`.
Invalid JSON, missing/invalid fields, unsupported options/masks: HTTP 400 /
`INVALID_ARGUMENT`. Unknown place IDs: 404 / `NOT_FOUND`. Storage failures: 500 /
`INTERNAL`, with diagnostic details only in service logs. Wrong methods: 405 with
`Allow`; unknown routes: 404. Bodies are limited to 16 KiB.

This local demo has no API-key enforcement, quotas, billing or OAuth. Optional
`key` query parameters and `X-Goog-Api-Key` headers are accepted as compatibility
credentials without authentication. Do not treat it as a protected public service.
These limits and unsupported-option errors are deliberate subset boundaries,
not a claim to reproduce every Google validation rule or error message.
