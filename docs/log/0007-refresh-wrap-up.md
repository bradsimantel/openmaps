# Refresh milestone wrap-up

2026-09-07 · Refresh milestone working tree based on `72440e1`.

The desktop browser verification in [0006](0006-desktop-browser-verification.md)
identified a static August label that remained visible during July activation.
Changed that footer to “Pinned source snapshots”; it now stays accurate across
activation and rollback without claiming to show the active release. `/healthz`
remains the source for the loaded database fingerprint.

Updated the maintained refresh guide to use the verified Codex in-app browser
connection. The external Chrome extension issue remains a separate tool issue;
it does not block the local refresh workflow verified through the in-app browser.

These wrap-up changes affect static text and documentation only. Content and
formatting were reviewed, including the served footer and `git diff --check`.
Existing Go, regional rebuild, HTTP and browser verification results are recorded
in [0005](0005-newport-refresh-verification.md) and [0006](0006-desktop-browser-verification.md).
Large data and generated screenshots remain excluded from Git. No commit was
made during this review.
