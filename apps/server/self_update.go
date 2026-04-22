package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const githubRepo = "AmirSoleimani/openberth"

// releaseSigningPubKey is the ed25519 public key that signs release
// artifacts. Hex-encoded (64 chars = 32 bytes). Empty at boot until the
// release signing ceremony has been performed and the real key is
// committed — the update path refuses to run without a configured key.
//
// Operators generate a keypair ONCE with:
//
//	openssl genpkey -algorithm Ed25519 -out release-priv.pem
//	# extract raw 32-byte public key (see docs/signing.md) and paste here
//
// Private key stays in an offline-friendly secret store; each release
// CI run signs the artifact with it and publishes <artifact>.sig alongside.
var releaseSigningPubKey = ""

func selfUpdate(args []string) {
	checkOnly := false
	targetVersion := ""

	for i, a := range args {
		switch a {
		case "--check":
			checkOnly = true
		case "--version":
			if i+1 < len(args) {
				targetVersion = args[i+1]
			}
		}
	}

	fmt.Printf("Current version: %s\n", version)

	if targetVersion == "" {
		// Fetch latest release from GitHub API
		latest, err := fetchLatestVersion()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to check for updates: %v\n", err)
			os.Exit(1)
		}
		targetVersion = latest
	}

	fmt.Printf("Latest version:  %s\n", targetVersion)

	// Normalize for comparison (strip 'v' prefix)
	current := strings.TrimPrefix(version, "v")
	target := strings.TrimPrefix(targetVersion, "v")

	if current == target {
		fmt.Println("Already up to date.")
		return
	}

	if checkOnly {
		fmt.Printf("\nUpdate available: %s → %s\n", version, targetVersion)
		fmt.Println("Run 'sudo berth-server update' to install.")
		return
	}

	// Check we can write to our own binary
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot determine binary path: %v\n", err)
		os.Exit(1)
	}

	// Download new binary
	arch := runtime.GOARCH
	filename := fmt.Sprintf("berth-server-linux-%s", arch)
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", githubRepo, targetVersion, filename)
	sigURL := url + ".sig"

	fmt.Printf("Downloading %s...\n", filename)

	// Stage downloads to a private directory (0700) instead of world-writable
	// /tmp — prevents a local attacker from swapping in a symlink between
	// the download and the later chmod/rename.
	stageDir, err := updateStageDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to prepare update staging: %v\n", err)
		os.Exit(1)
	}
	tmpPath := filepath.Join(stageDir, filename)
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, 0600)
	if err != nil {
		// EXCL failed → stale file. Remove and retry once.
		os.Remove(tmpPath)
		tmpFile, err = os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, 0600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open staging file: %v\n", err)
			os.Exit(1)
		}
	}
	defer os.Remove(tmpPath) // cleanup on failure

	resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Download failed: HTTP %d (check version tag exists)\n", resp.StatusCode)
		os.Exit(1)
	}

	n, err := io.Copy(tmpFile, resp.Body)
	tmpFile.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Downloaded %.1f MB\n", float64(n)/1024/1024)

	// Signature verification. The release process must publish a .sig file
	// alongside each binary (raw 64-byte Ed25519 signature over the file
	// bytes). Without a configured public key or a matching signature,
	// refuse to proceed — self-update runs as root and a swapped binary
	// grants host-wide RCE.
	sigPath := tmpPath + ".sig"
	if err := downloadFile(sigURL, sigPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to download signature (%s): %v\n", sigURL, err)
		os.Exit(1)
	}
	defer os.Remove(sigPath)
	if err := verifyReleaseSignature(tmpPath, sigPath); err != nil {
		fmt.Fprintf(os.Stderr, "Signature verification failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "Refusing to install unverified binary. See docs/signing.md.")
		os.Exit(1)
	}
	fmt.Println("Signature verified.")

	// Make executable
	if err := os.Chmod(tmpPath, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to chmod: %v\n", err)
		os.Exit(1)
	}

	// Replace binary
	if err := os.Rename(tmpPath, exePath); err != nil {
		// Cross-device rename — copy instead
		src, err2 := os.Open(tmpPath)
		if err2 != nil {
			fmt.Fprintf(os.Stderr, "Failed to open downloaded binary: %v\n", err2)
			os.Exit(1)
		}
		dst, err2 := os.OpenFile(exePath, os.O_WRONLY|os.O_TRUNC, 0755)
		if err2 != nil {
			src.Close()
			fmt.Fprintf(os.Stderr, "Failed to write binary (try with sudo): %v\n", err2)
			os.Exit(1)
		}
		io.Copy(dst, src)
		src.Close()
		dst.Close()
	}

	fmt.Printf("Updated to %s\n", targetVersion)

	// Restart systemd service if running under it
	if isSystemdService() {
		fmt.Println("Restarting openberth service...")
		if err := exec.Command("systemctl", "restart", "openberth").Run(); err != nil {
			fmt.Printf("Warning: could not restart service: %v\n", err)
			fmt.Println("Restart manually: sudo systemctl restart openberth")
		} else {
			fmt.Println("Service restarted")
		}
	}
}

func fetchLatestVersion() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	if release.TagName == "" {
		return "", fmt.Errorf("no releases found")
	}

	return release.TagName, nil
}

func isSystemdService() bool {
	// Check if we're running under systemd by looking for INVOCATION_ID env
	// or checking if the openberth service exists
	if os.Getenv("INVOCATION_ID") != "" {
		return true
	}
	err := exec.Command("systemctl", "is-active", "--quiet", "openberth").Run()
	return err == nil
}

// updateStageDir returns a private (0700) directory under the user cache
// root in which to stage update downloads. Never /tmp — a local
// unprivileged user can pre-create symlinks matching predictable names
// in /tmp between our O_CREAT and the final rename.
func updateStageDir() (string, error) {
	// Try DATA_DIR/updates first (server runs under systemd with a
	// dedicated data dir). Fall back to user cache.
	if dataDir := os.Getenv("DATA_DIR"); dataDir != "" {
		dir := filepath.Join(dataDir, "updates")
		if err := os.MkdirAll(dir, 0700); err == nil {
			return dir, nil
		}
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "openberth", "updates")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// downloadFile fetches url into path. Used for the .sig side-download.
func downloadFile(url, path string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, 0600)
	if err != nil {
		os.Remove(path)
		f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// verifyReleaseSignature checks that sigPath contains a valid Ed25519
// signature over the contents of binPath, signed by releaseSigningPubKey.
func verifyReleaseSignature(binPath, sigPath string) error {
	if releaseSigningPubKey == "" {
		return errors.New("release signing public key is not configured in this build; this binary cannot verify updates (see docs/signing.md)")
	}
	pub, err := hex.DecodeString(releaseSigningPubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("malformed release signing public key (want %d-byte hex)", ed25519.PublicKeySize)
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("read signature: %w", err)
	}
	// Strip trailing whitespace/newline if the release tooling added one.
	sig = stripTrailingNewline(sig)
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("signature wrong size (got %d, want %d)", len(sig), ed25519.SignatureSize)
	}
	bin, err := os.ReadFile(binPath)
	if err != nil {
		return fmt.Errorf("read binary: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), bin, sig) {
		return errors.New("ed25519 verification failed (tampered binary or wrong key)")
	}
	return nil
}

func stripTrailingNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
