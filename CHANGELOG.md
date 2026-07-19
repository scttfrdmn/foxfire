# Changelog

All notable changes to foxfire are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-07-18

First tagged release. A Go client for the Philips Hue CLIP API v2, validated
end-to-end against real hardware (a Hue Bridge Pro with a lightstrip, motion
sensor, dimmer, smart button, and smart plug).

### Added
- **Discovery and pairing** — mDNS (`_hue._tcp`) with cloud-discovery fallback,
  the link-button handshake (`Pair`, `PairWait`), and TLS certificate pinning
  with trust-on-first-use (`PeerFingerprint`, `WithBridgeID`, `WithRootCA`,
  `WithPinnedFingerprint`).
- **Transport** — generic `getMany`/`getOne`/`put`/`post`/`del`; a 200 response
  carrying a populated `errors` array is treated as an error; separate token
  buckets for light and grouped-light command rates.
- **Lights and groups** — `Light`, `GroupedLight`, `Room`, `Zone`; scene recall,
  creation, and deletion; zone creation and deletion.
- **Sensors** — `Motion`, `GroupedMotion`, `Temperature`, `LightLevel`,
  `DevicePower`.
- **Devices** — `Button`/`ButtonService`, bridge config (`BridgeInfo`),
  `ZigbeeConnectivity`, and device rename.
- **Event stream** — `Subscribe` returns batches over a channel, with jittered
  exponential-backoff reconnection that recovers across a bridge reboot.
- **`foxfire/state`** — folds sparse event deltas into current state.
- **`foxfire/color`** — sRGB↔xy conversion at the D65 white point, hex parsing,
  and gamut clamping.
- **`Light.IsPlug`** — distinguishes a smart plug (a light with archetype
  `plug`) from a dimmable bulb.
- **CLI** (`cmd/foxfire`) — `discover`, `pair`, `lights`, `rooms`, `on`/`off`
  (room or individual light), `set` (brightness and color), `bridge`,
  `sensors`, `devices`, `rename`, and `watch`; a global `--json` flag on the
  listing commands.

### Known limitations
- The Entertainment API (DTLS-PSK streaming over UDP) is out of scope; if it
  lands it will be a separate, separately-versioned `foxfire/entertainment`.
- Test coverage is around 33%; most of the gap is CLI plumbing and error paths.

[Unreleased]: https://github.com/scttfrdmn/foxfire/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/scttfrdmn/foxfire/releases/tag/v0.1.0
