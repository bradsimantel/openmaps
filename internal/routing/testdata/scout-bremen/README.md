# Bremen Scout integration fixture

`lock.json` is the stable input for the opt-in downloaded-provider integration
tests. It is copied from the source pins recorded by the historical
`docs/log/0029-osm-scout-go-compatibility.md` experiment so later documentation
maintenance cannot silently change executable test inputs.

The downloaded package bytes remain outside Git and are selected with
`OPENMAPS_SCOUT_DIR`. This fixture is provider compatibility evidence, unlike the
small synthetic packages in the sibling `scout/` directory.
