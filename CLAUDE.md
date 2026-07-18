# CLAUDE.md

Working notes for Claude Code sessions on this repository. Read this before
making changes.

## What this is

`foxfire` is a Go client for the Philips Hue CLIP API v2. Module path is
`github.com/scttfrdmn/foxfire`. Apache 2.0.

The scaffold is complete and green. Milestones M0 (discovery, pairing, TLS),
M1 (typed resources), M2 (event stream), and M4 (state cache) are done. M3
(sensors) is partial and M5 (CLI) is a working smoke test rather than a
finished tool.

## Commands

```
make build      # build library and CLI
make test       # go test -race ./...
make cover      # coverage summary
make fmt        # gofmt -w .
make lint       # go vet + golangci-lint
```

CI runs gofmt, vet, race tests, and a CLI build across Go 1.22 and 1.23.
A change that does not survive `make test && make lint` is not done.

## Non-negotiable invariants

These are the things that will silently break the library if violated. Each
has a test guarding it; if you find yourself editing one of those tests to
make a change pass, stop and reconsider the change.

**1. Update fields are pointers.** The bridge applies partial PUTs. A field
present in the JSON is a command; a field absent means "leave alone". If a
new update struct gains a non-pointer field, the zero value of that struct
becomes a live command — brightness 0, or worse, `on: false` on every write.
Guarded by `TestPartialUpdateOmitsUnsetFields` and `TestZeroBrightnessIsSent`.

**2. State reconciliation must not clobber absent fields.** Events are sparse
deltas, not snapshots. Folding an on/off event must leave brightness alone.
Guarded by `TestApplyPreservesUnmentionedFields`.

**3. A 200 with a populated `errors` array is an error.** The bridge reports
partial failures this way. Status code alone is never sufficient. Guarded by
`TestErrorsArrayOnSuccessStatus`.

**4. `New` refuses an unstated TLS posture.** The bridge certificate has no
SAN matching the dialed IP, so stock verification can never succeed and any
permissive default would become load-bearing everywhere. Do not add one.
Guarded by `TestNewRequiresExplicitTLSPosture`.

**5. Rate limits stay in the client.** The bridge drops excess commands
silently rather than erroring, so callers cannot discover the ceiling
empirically. Roughly 10/s for `/resource/light`, 1/s for
`/resource/grouped_light` and scene recalls. New command endpoints must pick
a bucket; non-command writes (renames, scene edits) pass `nil`.

## Layout

```
transport.go     generic getMany[T] / getOne[T] / put; error mapping
client.go        construction, options, token buckets, service wiring
tls.go           bridge certificate verification and fingerprint pinning
discovery.go     mDNS (_hue._tcp) with cloud fallback
pair.go          link-button handshake (the one v1 /api endpoint left)
resource.go      Ref, Metadata, envelopes, ResourceType constants
light.go         Light, GroupedLight, and their services
group.go         Room, Zone, Scene, Device, Motion, Temperature
events.go        SSE subscription, reconnect, typed Event
internal/sse/    minimal SSE frame reader
state/           delta fold into current state (separate package on purpose)
cmd/foxfire/     CLI smoke test
```

Adding a resource type follows a fixed shape: define the wire struct, define
a `FooService struct{ c *Client }` with `List`/`Get` over
`getMany[Foo]`/`getOne[Foo]`, wire it into `Client` in `New`. No codegen, no
reflection. If a new resource needs a mutation, decide its rate bucket
explicitly.

## Conventions

- Go 1.22 is the floor. Generics on package-level functions only — Go does
  not permit type parameters on methods, which is why the transport helpers
  are functions taking `*Client` rather than methods.
- Comments explain *why*, not *what*. The existing comments are load-bearing
  documentation of bridge quirks; do not strip them as noise.
- Errors wrap sentinels so `errors.Is` works on the cases callers branch on.
  New failure modes that callers might reasonably handle get a sentinel.
- No new dependencies without a real reason. Current set is `zeroconf` for
  mDNS and `x/time/rate` for the limiters. IDs are strings rather than parsed
  UUIDs specifically to avoid a third.
- The Entertainment API is out of scope. It is DTLS-PSK over UDP at 50Hz — a
  different transport with different failure modes. If it happens it happens
  as `foxfire/entertainment`, separately versioned.

## Testing without hardware

`newFakeBridge` in `client_test.go` stands up an `httptest.NewTLSServer` and
injects its transport via `WithTransport`, which bypasses the bridge-identity
machinery. That is deliberate: certificate verification is tested separately
from protocol behaviour. Set `WithRateLimits(1000, 1000)` in tests or they
will sit on the token bucket.

Coverage is around 33%. Most of the gap is CLI plumbing and error paths.
Raising it is worth doing but not by testing getters.

## Open work, roughly in order

**M3, finish sensors.** `light_level` and `button` resources have no service.
`ButtonReport` decodes on the event stream but there is no `ButtonService`.
Also missing: `device_power` (battery level), `zigbee_connectivity` (reachable
state). The last one matters — commands to an unreachable light succeed at the
API level and do nothing physically, which is a confusing failure to debug.

**Hardware validation.** Nothing here has touched a real bridge. The kill
criterion is: pair, then run `foxfire watch` across a bridge reboot and
confirm the reconnect loop recovers. Second: a motion sensor firing produces
an event within a second or so. Until that passes, treat the event stream
code as plausible rather than working.

**Scene creation and editing.** Currently recall-only. Creating scenes means
POST support in the transport, which does not exist yet.

**Color helpers.** The wire type is CIE xy, which nobody thinks in. An
`xy <-> RGB` conversion with gamut clamping belongs in a subpackage, not in
the wire types. Note that out-of-gamut xy is not an error — the light clamps
silently, which is a common source of "the color is wrong" confusion.

**Bridge config resource.** No access to `/resource/bridge`, so there is no
way to read firmware version or the bridge's own ID post-pairing.

## Things that will waste your time if you do not know them

- Color temperature is mireds, not kelvin. `mirek = 1e6 / kelvin`.
- Brightness is a percentage and zero does not mean off. The bridge clamps to
  `min_dim_level`. Turning a light off goes through `on`.
- Writing to a room's `grouped_light` is dramatically cheaper than iterating
  members — the bridge issues a Zigbee multicast instead of N unicasts.
- A room's grouped light is in `services`, not `children`. `children` holds
  devices. `Room.GroupedLightID()` exists for this.
- Rooms group devices; zones group light services. A light belongs to one
  room and any number of zones.
- The stream's per-request timeout has to be disabled or the client-wide
  timeout guillotines it. See the separate `streamClient` in `streamOnce`.
- Pairing hits `/api`, not `/clip/v2`, and returns the awkward v1 array-of-
  single-key-objects shape. That is the only place it survives.
