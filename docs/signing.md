# Release signing

OpenBerth's self-update (`berth-server upgrade`, `berth upgrade`) refuses to replace a running binary unless the download is signed by the private half of the key whose public half is compiled into the binary. This document is the operator playbook: how the key was installed, how to verify a release, and how to rotate.

## What's in the repo

- **Public key** (hex-encoded ed25519, 32 bytes): `ffd5a9dc2b0e8b4390bc98a0d0b99f4c325871f6ce7ed13514d01e9f0af0a362`
  - `apps/server/self_update.go` — `releaseSigningPubKey` constant.
  - `apps/cli/self_update.go` — same value.
- **Signing workflow**: `.github/workflows/release.yml`, "Sign artifacts" step. Runs on every tag push after the builds. Writes `<artifact>.sig` next to each binary in `dist/` before the release is created.
- **Verification code**: `verifyReleaseSignature` in both `self_update.go` files. Called before `chmod` + `rename`.

## GitHub Actions secret

The signing step reads one secret:

| Name | Value |
|---|---|
| `RELEASE_SIGNING_PRIVATE_KEY` | ed25519 private key, hex-encoded (128 hex chars = 64 bytes — the Go ed25519 `PrivateKey` is `seed \|\| pub`) |

Set it once:

```
gh secret set RELEASE_SIGNING_PRIVATE_KEY
# paste the hex string when prompted
```

Or via the GitHub UI: repo → Settings → Secrets and variables → Actions → New repository secret.

If the secret isn't set, the release step fails fast with a clear error — no unsigned binaries ship.

## Verifying a release manually

Any operator can verify a downloaded artifact without trusting `berth upgrade`:

```sh
# Download the binary and its signature
curl -LO https://github.com/AmirSoleimani/openberth/releases/download/v1.2.3/berth-server-linux-amd64
curl -LO https://github.com/AmirSoleimani/openberth/releases/download/v1.2.3/berth-server-linux-amd64.sig

# Verify with a one-liner
cat > /tmp/verify.go <<'EOF'
package main

import (
    "crypto/ed25519"
    "encoding/hex"
    "fmt"
    "os"
)

func main() {
    const pubHex = "ffd5a9dc2b0e8b4390bc98a0d0b99f4c325871f6ce7ed13514d01e9f0af0a362"
    pub, _ := hex.DecodeString(pubHex)
    bin, _ := os.ReadFile(os.Args[1])
    sig, _ := os.ReadFile(os.Args[2])
    if ed25519.Verify(ed25519.PublicKey(pub), bin, sig) {
        fmt.Println("OK")
    } else {
        fmt.Println("FAIL")
        os.Exit(1)
    }
}
EOF
go run /tmp/verify.go berth-server-linux-amd64 berth-server-linux-amd64.sig
```

## Rotation

Compromise, expected or otherwise, means a full rotation:

1. **Generate a new keypair** offline (not on a production host):
   ```sh
   cat > /tmp/gen.go <<'EOF'
   package main
   import (
       "crypto/ed25519"
       "encoding/hex"
       "fmt"
   )
   func main() {
       pub, priv, _ := ed25519.GenerateKey(nil)
       fmt.Printf("PUBLIC=%s\nPRIVATE=%s\n", hex.EncodeToString(pub), hex.EncodeToString(priv))
   }
   EOF
   go run /tmp/gen.go
   rm /tmp/gen.go
   ```
2. **Replace `releaseSigningPubKey`** in both `apps/server/self_update.go` and `apps/cli/self_update.go` with the new `PUBLIC` value. Commit to a feature branch.
3. **Update the secret**: `gh secret set RELEASE_SIGNING_PRIVATE_KEY` with the new `PRIVATE` value.
4. **Ship a release** from the rotation branch. The workflow will sign with the new private key; downloaded binaries will carry the new public key.
5. **Announce the rotation**. Tell operators: upgrades from pre-rotation binaries must be done manually (see "One-time transition" below). Automatic upgrades resume once every host runs a post-rotation binary.
6. **Destroy the old private key**. If it was compromised, wipe every copy.

There is intentionally no overlap period where the binary trusts both keys. If you need one (e.g., during a staged rollout), compile a transitional binary with a small embedded public-key list. Default is one key at a time to keep the attack surface small.

## One-time transition: unsigned → signed

The first signed release after this PR is unreachable via `berth upgrade` from any pre-signing binary — those binaries either don't verify anything (in which case they'd auto-upgrade successfully, defeating the point) or already refuse unsigned updates (blocking the transition). To cross:

1. Download the new signed binary manually.
2. Verify the signature with the one-liner in "Verifying a release manually" above, or simply `sha256sum` against the release notes.
3. Replace the existing binary in place (`sudo mv berth-server-linux-amd64 /usr/local/bin/berth-server && sudo systemctl restart openberth`).
4. All subsequent `berth-server upgrade` invocations verify automatically.

After the transition, every host runs a binary that carries `releaseSigningPubKey`, and the automatic upgrade path is secure end-to-end.

## Key handling rules

- The private key lives **only** in the GitHub Actions secret and whatever offline backup you keep. It never lives in the repo, a developer's laptop, or CI logs.
- The signing step's output (`<artifact>.sig`) is public — signatures don't reveal anything about the key.
- Public keys rotate only on compromise or scheduled rotation. Day-to-day there's one key.
- Never accept an unsigned binary from `berth upgrade`. If the signature fetch or verification fails, `verifyReleaseSignature` returns an error and the binary is not chmod'd or moved. The existing installation keeps running.
