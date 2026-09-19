# Implementation research

Done 2026-09-18, before any app code. It answers: how to build the servers, how to handle passwords, how devices find each other on a network, what other connection types exist, whether one framework does all of it, and what related open-source projects teach.

Anything that could not be confirmed is marked **unverified**.

## 1. Is there a universal framework?

No. Nothing does capture, hardware encoding and RTSP/ONVIF serving well on Android, iOS, macOS and Windows.

| Candidate | Why not |
|---|---|
| [GStreamer](https://gstreamer.freedesktop.org/) | The only one covering all four. Its Android camera source still uses the deprecated Camera1 API (the Camera2 source was never merged), a full build is ~49 MB (~3 MB hand-trimmed), and static LGPL linking on iOS is legally unsettled. |
| FFmpeg libraries | Capture and encode everywhere, but [cannot serve RTSP](https://ffmpeg.org/ffmpeg-protocols.html): the muxer only pushes, and listen mode accepts one publisher. |
| libwebrtc, LiveKit | WebRTC only. Blue Iris and Synology cannot pull WebRTC. |
| Flutter, React Native, Kotlin Multiplatform, Tauri 2 | Camera plugins hand back a preview, files or raw frames. None exposes the encoder's input surface or encoded frames. |

So capture and encoding are native per platform, and the camera writes directly into the hardware encoder (Camera2 → MediaCodec input surface; AVCaptureSession → VideoToolbox).

## 2. Servers

### RTSP libraries

| Library | License | State |
|---|---|---|
| [gortsplib](https://github.com/bluenviron/gortsplib) (Go) | MIT | The most complete RTSP server library in any language, and the engine inside MediaMTX. v5.6.5, ~20 releases in 12 months. Pure Go, needs Go 1.26. |
| [rtsp-types](https://crates.io/crates/rtsp-types) (Rust) | MIT | Message parsing only. Last release 0.1.3, 2024. |
| [rtp](https://docs.rs/rtp) (Rust, webrtc-rs) | MIT/Apache | H.264 and H.265 payloaders, no AAC. 0.17 is bug-fix only since webrtc-rs moved to v0.20. |
| [retina](https://github.com/scottlamb/retina) (Rust) | MIT | Client only. |
| [RTSP-Server](https://github.com/pedroSG94/RTSP-Server) (Kotlin) | Apache-2.0 | Android only. Takes MediaCodec output directly; **Basic auth only**, no `GET_PARAMETER` handler. |
| [HaishinKit](https://github.com/HaishinKit/HaishinKit.swift) (Swift) | BSD | **No RTSP.** RTMP, SRT and WHIP only. |
| gst-rtsp-server, live555 (C/C++) | LGPL | Work, but static linking on iOS is legally unsettled. |

**No maintained open-source iOS RTSP server exists.** The iOS samples on GitHub (teocci, irons163) were last touched in 2017 and 2021.

### What NVRs need from an RTSP server

- **TCP interleaved is mandatory**; Frigate and go2rtc default to it. UDP is also required for ONVIF conformance.
- **`sprop-parameter-sets` and `profile-level-id` in the SDP, and SPS/PPS (plus VPS for HEVC) resent in-band before every IDR.** Missing these produce "non-existing PPS" or a black picture ([go2rtc #2202](https://github.com/AlexxIT/go2rtc/issues/2202)).
- **Keepalive:** answer both `OPTIONS` and `GET_PARAMETER`, and send `Session: …;timeout=60`.
- **RTCP sender reports:** ffmpeg needs them for audio/video sync, Blue Iris for timecodes.
- **Several clients at once**, with per-client queues, so a slow reader never stalls the encoder.
- Force a keyframe when a client joins; keep a 2 s GOP.
- **Audio: AAC.** Frigate transcodes anything else.

### ONVIF device side

Formal [Profile S](https://www.onvif.org/wp-content/uploads/2019/12/ONVIF_Profile_-S_Specification_v1-3.pdf) conformance needs ONVIF membership and mandates MJPEG over RTSP, and Profile S is [sunset on 31 March 2027](https://www.onvif.org/profiles/profile-s/profile-s-deprecation-qna/). Not worth chasing.

What real clients call is much smaller. [go2rtc's ONVIF server](https://github.com/AlexxIT/go2rtc/blob/master/pkg/onvif/server.go) answers ~28 operations as string templates and is tested against ONVIF Device Manager, Home Assistant, Happytime and Onvier. The useful set:

- GetSystemDateAndTime, GetCapabilities, GetServices, GetDeviceInformation, GetScopes, GetNetworkInterfaces
- GetVideoSources, GetProfiles, GetStreamUri, GetSnapshotUri, and the encoder-configuration getters

Pitfalls other projects hit:

- **UniFi Protect needs a filled-in `RateControl`, `VideoSourceConfiguration` and `Resolution`.** An empty `RateControl` fails adoption with "Channel fps is not found" ([go2rtc #1994](https://github.com/AlexxIT/go2rtc/issues/1994), [PR #1520](https://github.com/AlexxIT/go2rtc/pull/1520)). A later change removed it again and broke UniFi in a release.
- **UniFi and Home Assistant key cameras on identity**: MAC address, serial, EndpointReference UUID. Keep them stable ([daniela-hase/onvif-server](https://github.com/daniela-hase/onvif-server)).
- **Every VMS probes a different set of operations** ([rpos](https://github.com/BreeeZe/rpos) kept adding calls for new ones).
- Build the stream URI from the request's Host header.
- Return a fault for unknown profile tokens.

## 3. Passwords and authentication

### RTSP

Offer **Digest MD5** with a fixed realm, the header style Hikvision and Dahua use. Keep Basic as an opt-in compatibility switch, and never advertise both at once ([ExoPlayer #9800](https://github.com/google/ExoPlayer/issues/9800)).

- Accept both `qop=auth` and the older form without `qop`: [ffmpeg](https://raw.githubusercontent.com/FFmpeg/FFmpeg/master/libavformat/httpauth.c) sends `qop` when offered; go2rtc and live555 never do.
- Compute the hash from the `uri` in the Authorization header.
- Keep one nonce per session; answer `stale=TRUE` when it expires.
- **No client supports SHA-256 Digest for RTSP.** gortsplib defaults to MD5 because SHA-256 "prevents FFmpeg from authenticating".
- A username must never contain `:`; ffmpeg splits on the first one.

### ONVIF

Accept **both** WS-UsernameToken and HTTP Digest (MD5). The [Core spec](https://www.onvif.org/specs/core/ONVIF-Core-Specification.pdf) requires HTTP Digest and treats UsernameToken as legacy, while Frigate and Anivar send UsernameToken.

- The digest is `Base64(SHA-1(nonce + created + password))`, with the nonce base64-decoded to raw bytes first. **SHA-1, not SHA-256** — this is exactly the bug found in Anivar's client ([anivar #20](https://github.com/anivarhq/anivar/pull/20)).
- Reject tokens missing a nonce or Created. Allow a ±300 s window and keep a nonce cache for it.
- **Unauthenticated by design:** GetSystemDateAndTime, GetCapabilities, GetServices, GetWsdlUrl, GetEndpointReference, and the discovery probes. **Not GetUsers** — [onvif_simple_server](https://github.com/roleoroleo/onvif_simple_server) leaves that open.
- Answer a credential-less protected call with a 401 Digest challenge.
- The best reference implementation read: [onvif-rs `src/auth.rs`](https://github.com/mickeyzzc/onvif-rs) (MIT) — constant-time compare, Created window, replay cache.

### Storing the password

WS-UsernameToken needs the plaintext, and for Digest the stored HA1 is equivalent to the password, so **hashing is not possible**. Store the plaintext encrypted with a key the OS holds. That is acceptable because the app generates the password, so it behaves like an API key rather than a reused human password.

- **Android:** DataStore + Tink with an Android Keystore key, excluded from backup. EncryptedSharedPreferences was [deprecated in 2025](https://blog.includesecurity.com/2026/08/encryptedsharedpreferences-is-dead-heres-what-you-should-use-instead/). If decryption fails, regenerate and re-pair.
- **Apple:** Keychain with [`kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`](https://developer.apple.com/documentation/security/ksecattraccessibleafterfirstunlockthisdeviceonly).
- **Windows:** DPAPI or Credential Manager.

### Generating credentials

- Username fixed, e.g. `admin`.
- Password: 16 random characters from `[A-Za-z0-9]` minus look-alikes, about 93 bits. It survives URL embedding with no escaping and fits NVR fields that cap at 16 characters. Special characters have broken [Frigate](https://github.com/blakeblackshear/frigate/issues/4974) and ZoneMinder before.
- Show it once on a QR code containing the stream URL, with a Copy button, a re-reveal behind device unlock, and a Rotate action.
- Precedent: [Eufy](https://service.eufy.com/article-description/Device-NAS-RTSP-Configuration-Guide) generates random credentials; UniFi Protect keeps RTSP off per camera until enabled.
- Law: the UK PSTI Act and California SB-327 ban universal default passwords for devices; [ETSI EN 303 645](https://www.etsi.org/deliver/etsi_en/303600_303699/303645/03.01.02_20/en_303645v030102a.pdf) requires per-device randomness and brute-force protection. The EU Cyber Resilience Act's Annex I applies from **11 December 2027** and does cover commercial software. *(Reading of the law, not legal advice.)*

### Hardening

- **Throttle per source IP, never lock the account.** Hikvision's per-account lock lets anyone on the network lock out your own NVR. Axis blocks per IP. Exempt addresses that authenticated in the last 24 hours so a reconnecting NVR is never blocked; count only wrong credentials; compare in constant time.
- **No localhost exemption** (go2rtc has one). On Android any app can reach 127.0.0.1.
- Listen on Wi-Fi/Ethernet only, never cellular.
- Accept only private source addresses: RFC 1918, link-local, `fc00::/7`, plus `100.64.0.0/10` for Tailscale. Phones often hold globally reachable IPv6 addresses.
- Never open a router port via UPnP. [Bitsight found 40,000+](https://www.bitsight.com/blog/bitsight-identifies-thousands-of-compromised-security-cameras) openly streaming cameras in 2025.

### Encryption on the network

None in v1; the LAN password is protected by Digest and the video is plaintext. For v2, optional RTSPS with a self-signed certificate whose fingerprint is pinned through the pairing QR code. ffmpeg does not verify certificates by default; go2rtc offers `rtspx://` to skip verification; **Blue Iris has no RTSPS client**. Land this before the CRA date above.

## 4. Finding each other on a network

| NVR | How it discovers cameras |
|---|---|
| Blue Iris, Synology, UniFi Protect, Home Assistant, go2rtc | ONVIF WS-Discovery |
| Frigate | typed IP address only |

Nothing browses mDNS or SSDP for generic cameras, so both are skippable. Home Assistant reads the `name`, `hardware` and `Profile/Streaming` scopes from the reply.

**The responder should:** send Hello at start and on IP change, Bye at stop, answer Probes with a unicast ProbeMatch that echoes the probe's MessageID, and keep a `urn:uuid` EndpointReference that never changes. Behaviour modelled on `wsd_simple_server.c`, which is **GPL — read only, don't copy**.

### Platform rules

- **iOS** ([TN3179](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy)): accepting incoming TCP needs no permission, so the RTSP and ONVIF servers work unprompted. Sending anything triggers the Local Network prompt. Multicast, in either direction, needs [`com.apple.developer.networking.multicast`](https://developer.apple.com/contact/request/networking-multicast), granted per team in roughly 3–5 days (up to 2 weeks). Without it the device can still answer a **unicast** Probe.
- **Android**: hold a `MulticastLock`; `NsdManager` cannot set a custom hostname; apps targeting **API 37 need `ACCESS_LOCAL_NETWORK` even to accept incoming TCP** ([docs](https://developer.android.com/privacy-and-security/local-network-permission)). A camera foreground service keeps the stream alive with the screen off and is exempt from network restrictions, but cannot start from `BOOT_COMPLETED`.
- **macOS 15+**: Local Network privacy applies; no multicast entitlement needed.
- **Windows**: inbound is blocked until a rule exists, and new networks default to Public. The installer should add program-scoped rules for TCP 8554/8000 and UDP 3702/5353 on Private and Domain.

### Staying findable after a DHCP change

Advise a DHCP reservation and a fixed (not rotating) private Wi-Fi address. Later, an NVR can re-match a camera by its EndpointReference UUID when a Hello or ProbeMatch arrives. `.local` names do not resolve inside Docker containers such as Frigate.

## 5. Other connection types

| Type | Receivers | Verdict |
|---|---|---|
| **RTSP pull + ONVIF** | every NVR | **v1** |
| RTSP push (ANNOUNCE/RECORD) | go2rtc, MediaMTX, Frigate via go2rtc | v2; camera dials out, so no inbound ports, discovery or firewall issues |
| Snapshot JPEG / MJPEG | Blue Iris, Synology, Home Assistant, UniFi | v1.1; UniFi shows only a logo without a snapshot URI |
| WHIP / WHEP | go2rtc, MediaMTX, browsers | later |
| SRT | MediaMTX | skip |
| RTMP | go2rtc, MediaMTX | skip; H.264/AAC only |
| HomeKit | via a [go2rtc](https://go2rtc.org/internal/homekit/) or [Scrypted](https://docs.scrypted.app/homekit.html) bridge | never in-app; a real accessory needs MFi |
| Google Home / Alexa | cloud only | skip; watch Matter 1.5 cameras (local WebRTC) |
| Remote access | Tailscale on both ends | allow `100.64.0.0/10` |

Precedent: Synology's own phone-as-camera product (LiveCam) pushes to the NVR and costs one license per device. Alfred uses peer-to-peer WebRTC with a relay fallback.

## 6. Open-source projects read

| Project | License | Value | Lesson |
|---|---|---|---|
| [gortsplib](https://github.com/bluenviron/gortsplib) | MIT | best RTSP foundation | Set `AuthMethods` explicitly (Basic is on by default); prepend SPS/PPS yourself; its digest compare is not constant-time |
| [go2rtc](https://github.com/AlexxIT/go2rtc) `pkg/onvif` | MIT | ONVIF response templates | No ONVIF auth at all, no WS-Discovery responder; its RTSP server has no `GET_PARAMETER` case and drops the connection |
| [onvif-rs](https://github.com/mickeyzzc/onvif-rs) `auth.rs` | MIT | WSSE auth design | Too young to depend on (v0.3, one author) |
| [RTSP-Server / RootEncoder](https://github.com/pedroSG94/RTSP-Server) | Apache-2.0 | Android server, SDP building | Basic auth only; clients get a coroutine each with dropped-frame counters |
| [onvif_simple_server](https://github.com/roleoroleo/onvif_simple_server) | **GPL-3** | behaviour reference only | No replay check; GetUsers unauthenticated |
| [thingino](https://github.com/themactep/thingino-firmware) / Raptor | MIT / GPL | architecture | Separate capture from serving; bounded drop-oldest buffers; prudynt-t (live555 monolith) is archived |
| [libstreaming](https://github.com/fyhertz/libstreaming), spydroid | Apache / GPL | cautionary | Buffer-to-buffer MediaCodec plus UDP-only RTSP became unmaintainable; its `EncoderDebugger` exists purely to find each phone's working encoder/color-format combination |
| [android-ip-camera](https://github.com/DigitallyRefined/android-ip-camera), [lenscast](https://github.com/raulshma/lenscast), [IP_Cam](https://github.com/tobi01001/IP_Cam) | MIT / GPL | market check | Mostly MJPEG; where ONVIF exists it is unauthenticated |

The closest competitor, "Guardian Eye Web Camera" ([Frigate #23724](https://github.com/blakeblackshear/frigate/discussions/23724)), has almost this exact feature set and is closed source.

## 7. The shared-core option

The chosen architecture writes the server side once per platform. The alternative is **one shared core** that every platform's native layer feeds encoded frames into, keeping the same Flutter UI.

- **Go:** gortsplib for RTSP, an ONVIF responder modelled on go2rtc's templates, a WS-Discovery responder, bound with [`gomobile bind`](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile) for Android and Apple, and a plain binary on Windows. Roughly 300 lines of glue instead of 1.5–3k lines of RTSP code.
- **Rust:** `rtsp-types` plus the `rtp` payloaders, with UniFFI bindings. More code owned, on less actively maintained crates.

Costs of the Go route, all real: gomobile is officially experimental with no tagged releases; a Go panic crossing the binding kills the app, so every exported function needs `recover()`; only one gomobile framework per app; and **Go's output is not 16 KB page-aligned by default**, which Google Play has required since November 2025 (the issue was closed "not planned" [twice](https://github.com/golang/go/issues/81358)), so the build needs a link flag plus a CI check with `llvm-readelf`. FFI cost for 30–60 frames/s at 2–8 Mbps is negligible.

Production users of gomobile: Tailscale Android, Psiphon, Berty, Outline.

### The spike: it works (2026-09-19)

`core/` is that shared core, ~350 lines over gortsplib, and [CI](../.github/workflows/core.yml) builds it for both mobile platforms. Measured, not assumed:

| Question | Result |
|---|---|
| Serves a real player? | ffmpeg pulls 1280×720 at 15 fps over TCP, with Digest auth; a wrong password gets 401 |
| Keyframe on join? | fires |
| Builds for Apple? | xcframework for iOS, simulator and macOS, on a current Xcode |
| Builds for Android? | `.aar`, arm64, **3.26 MB** |
| 16 KB page-aligned? | yes, `0x4000`, checked in CI with `llvm-readelf` |

Two failures worth keeping: gomobile refuses to bind unless `golang.org/x/mobile` is a tool dependency of the module, and it still defaults to Android API 16, which NDK r28 rejects (21..35). Both are one-line fixes, now in the workflow.

Two bugs the local test caught, each of which would otherwise have surfaced much later: a `ServerStream` can only be initialized after the server has started, and an unauthorized response must return `liberrors.ErrServerAuth{}` — without it, gortsplib sends no challenge and even the correct password fails.

**So the iOS "write an RTSP server from scratch" job can be deleted from the plan.** The remaining decision — whether Android also uses this core, or RootEncoder's own server — is the user's.

## 8. Rules worth keeping whatever we build

1. **Encoders take surface or pixel-buffer input, never buffer-to-buffer.** That was libstreaming's undoing.
2. **Bounded, drop-oldest buffers between capture and serving.** A slow client must never stall the encoder.
3. **Stable identity**: a persisted UUID, serial and MAC. NVRs key on them.
4. **Truthful encoder configuration** in ONVIF, including RateControl.
5. **A golden corpus of recorded NVR request/response exchanges, replayed in tests.** Both go2rtc and daniela-hase shipped silent ONVIF regressions.
6. **Pin dependency versions** and let Dependabot raise them.

## What could not be verified

- gortsplib and the ONVIF code compiling under gomobile; no public example exists.
- Current gomobile and GStreamer binary sizes on mobile.
- Which protocol Synology's camera search uses.
- Whether Blue Iris or ffmpeg on Windows resolve `.local` names.
- The deprecation status of `WIFI_MODE_FULL_HIGH_PERF`.
- Incoming Tailscale connections on iOS.
- The exact ONVIF calls Blue Iris and Synology make; that needs a packet capture against a real device.
