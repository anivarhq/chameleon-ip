# Security

Chameleon IP listens on your network, so a flaw in it can matter. Please report
vulnerabilities **privately** through
[GitHub's advisory form](https://github.com/anivarhq/chameleon-ip/security/advisories/new),
not as a public issue.

A useful report says which platform and version, what an attacker needs (the
same network? a password?), and how to reproduce it. We'll confirm we have it,
say what we're doing about it, and credit you in the fix unless you'd rather
not be named.

## What the design already assumes

- Every stream and ONVIF request needs the device's generated password
  (Digest authentication).
- Connections from outside the local network are refused before any protocol
  runs: private and link-local addresses and Tailscale are accepted, nothing
  else.
- Password guessing is slowed per address, and an address that signed in
  within the last day is never slowed.
- The desktop app's picture reaches the window over a pipe and a loopback
  socket; it adds no port that faces the network.

Out of scope: someone who already controls the device or its user account.
