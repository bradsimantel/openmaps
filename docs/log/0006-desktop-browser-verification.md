# Desktop browser verification

2026-09-07 · Refresh milestone working tree based on `72440e1`; existing browser
client as served at verification time.

Historical follow-up to [0005](0005-newport-refresh-verification.md). Its browser
blocker was resolved for the Codex desktop **in-app browser**. The external Chrome
extension transport remains unresolved; these results do not claim Chrome
extension verification.

After filesystem/network permissions changed, `go run ./cmd/server -deployment
 data/deployment.json` started successfully on `127.0.0.1:8080`. The original tab
contained a cached connection-error document that the browser tool could not
navigate. A fresh in-app tab loaded the application successfully through the
installed browser plugin. No standalone Playwright or npm tooling was added.

## Observed flows

Used the visible search input, read autocomplete suggestions and clicked the
specific result, then inspected details, marker labels and actual screenshots.

- **White Horse Tavern:** `White Horse` returned one business suggestion.
  Selection showed 26 Marlborough St, coordinates latitude 41.491390,
  longitude −71.313731, website and precision note. The map rendered a visible
  marker and White Horse Tavern popup at that source location.
- **50 Bellevue Avenue:** `50 Bellevue` returned the standalone address first
  and Redwood Library and Athenaeum second. Selecting the address showed
  latitude 41.486544, longitude −71.308304 and address-point precision note.
  The marker and popup moved to Bellevue Avenue next to the library.
- Both complete flows passed on the August baseline and the reviewed July
  candidate. Browser error logs were empty on the inspected baseline/candidate
  flows. Real basemap tiles and labels rendered in the screenshots.
- Used the reviewed activation command, with health confirming July database
  SHA-256 `9b21afcefbd447ce5416ba86c4c32f44bdfde3904bc47a2567f44cff725bbc29`.
  July's Redwood Library suggestion showed postcode `02840-3229`.
- Rolled back, confirmed baseline SHA-256
  `b76ee297a476a67ed9883c1d5f8fb6ede17e5c4fba5525a3677600be824e3d13`, reloaded,
  and repeated address search/selection. Redwood's postcode returned to `02840`.
  Left the server running on August and the working tab open.

Screenshots are retained locally, excluded from Git:
`data/browser-desktop-business.png`, `data/browser-desktop-address.png`,
`data/browser-desktop-candidate-business.png`, and
`data/browser-desktop-candidate-address.png`.

The current client's footer says “August 2026 source snapshots” even while the
July candidate is active. That static caption is not a deployment indicator;
health fingerprints were used to verify the actual selection. The caption was
accurate again after rollback. No UI changes were made during these checks.
Mobile layout, keyboard navigation, CDN failure behavior and the external Chrome
extension were not reverified in this follow-up. Documentation received a content
and formatting review; no Go code changed in this follow-up.
