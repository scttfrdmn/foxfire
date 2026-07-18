// Package foxfire is a client for the Philips Hue CLIP API v2.
//
// Design notes:
//
//   - The bridge speaks HTTPS only (RED compliance, Aug 2025 firmware onward)
//     and presents a self-signed certificate whose Common Name is the bridge
//     ID. Verification is strict by default: see WithBridgeID and
//     WithSignifyRoot. Use WithInsecureTLS only for bring-up.
//
//   - Every mutation is a partial PUT. A field that is present is a command;
//     a field that is absent is "leave alone". Therefore every field on an
//     update struct is a pointer with omitempty, and the zero value of an
//     update struct is a well-defined no-op. Helpers Bool, Float, Int, and
//     String exist so callers are not littered with temporaries.
//
//   - The bridge is a small ARM device. It will silently drop commands past
//     roughly 10/s for individual lights and 1/s for grouped lights. The
//     client enforces separate token buckets for each so that callers do not
//     have to discover this empirically.
//
//   - The event stream is the primary way to observe state. Polling is
//     supported but discouraged. Events carry deltas, not full resources;
//     reconciliation into current state lives in the sibling package
//     foxfire/state so that consumers who only want raw events do not pay
//     for a cache they will not read.
//
// The Entertainment API (DTLS-PSK streaming over UDP) is deliberately out of
// scope for v1. It is a different transport with different failure modes.
package foxfire
