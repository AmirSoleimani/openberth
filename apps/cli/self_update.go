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

// releaseSigningPubKey — see apps/server/self_update.go for semantics
// and docs/signing.md for rotation procedure. Must match the server's
// release-signing pubkey; both binaries are signed by the same key.
var releaseSigningPubKey = "ffd5a9dc2b0e8b4390bc98a0d0b99f4c325871f6ce7ed13514d01e9f0af0a362"

func cmdUpdateCLI() {
	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth CLI Update%s\n\n", cBold, cReset)

	checkOnly := hasFlag("check")
	targetVersion := getFlag("version", "")

	info(fmt.Sprintf("Current version: %s", version))
	info(fmt.Sprintf("Platform:        %s/%s", runtime.GOOS, runtime.GOARCH))

	if targetVersion == "" {
		latest, err := fetchLatestRelease()
		if err != nil {
			fail(fmt.Sprintf("Failed to check for updates: %v", err))
			os.Exit(1)
		}
		targetVersion = latest
	}

	// Ensure version has v prefix for download URL
	if !strings.HasPrefix(targetVersion, "v") {
		targetVersion = "v" + targetVersion
	}

	info(fmt.Sprintf("Target version:  %s", targetVersion))

	// Compare versions (strip v prefix)
	current := strings.TrimPrefix(version, "v")
	target := strings.TrimPrefix(targetVersion, "v")

	if current == target {
		ok("Already up to date.")
		fmt.Println()
		return
	}

	if checkOnly {
		warn(fmt.Sprintf("Update available: %s -> %s", version, targetVersion))
		info("Run 'berth update-cli' to install.")
		fmt.Println()
		return
	}

	// Determine binary name
	osName := runtime.GOOS
	arch := runtime.GOARCH
	binaryName := fmt.Sprintf("berth-%s-%s", osName, arch)
	if osName == "windows" {
		binaryName += ".exe"
	}

	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", githubRepo, targetVersion, binaryName)
	sigURL := url + ".sig"

	spin("Downloading " + binaryName)

	stageDir, err := cliUpdateStageDir()
	if err != nil {
		fmt.Println()
		fail(fmt.Sprintf("Failed to prepare staging dir: %v", err))
		os.Exit(1)
	}
	tmpPath := filepath.Join(stageDir, binaryName)
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, 0600)
	if err != nil {
		os.Remove(tmpPath)
		tmpFile, err = os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, 0600)
		if err != nil {
			fmt.Println()
			fail(fmt.Sprintf("Failed to open staging file: %v", err))
			os.Exit(1)
		}
	}
	defer os.Remove(tmpPath)

	resp, err := http.Get(url)
	if err != nil {
		fmt.Println()
		fail(fmt.Sprintf("Download failed: %v", err))
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Println()
		fail(fmt.Sprintf("Download failed: HTTP %d (check version tag exists)", resp.StatusCode))
		os.Exit(1)
	}

	n, err := io.Copy(tmpFile, resp.Body)
	tmpFile.Close()
	if err != nil {
		fmt.Println()
		fail(fmt.Sprintf("Download failed: %v", err))
		os.Exit(1)
	}
	done()

	info(fmt.Sprintf("Downloaded %.1f MB", float64(n)/1024/1024))

	// Signature verification
	sigPath := tmpPath + ".sig"
	if err := cliDownloadFile(sigURL, sigPath); err != nil {
		fail(fmt.Sprintf("Failed to download signature: %v", err))
		os.Exit(1)
	}
	defer os.Remove(sigPath)
	if err := cliVerifyReleaseSignature(tmpPath, sigPath); err != nil {
		fail(fmt.Sprintf("Signature verification failed: %v", err))
		info("Refusing to install unverified binary. See docs/signing.md.")
		os.Exit(1)
	}
	info("Signature verified.")

	// Make executable
	if err := os.Chmod(tmpPath, 0755); err != nil {
		fail(fmt.Sprintf("Failed to chmod: %v", err))
		os.Exit(1)
	}

	// Determine current binary path
	exePath, err := os.Executable()
	if err != nil {
		fail(fmt.Sprintf("Cannot determine binary path: %v", err))
		os.Exit(1)
	}

	// Replace binary
	if err := os.Rename(tmpPath, exePath); err != nil {
		// Cross-device rename — copy instead
		src, err2 := os.Open(tmpPath)
		if err2 != nil {
			fail(fmt.Sprintf("Failed to open downloaded binary: %v", err2))
			os.Exit(1)
		}
		dst, err2 := os.OpenFile(exePath, os.O_WRONLY|os.O_TRUNC, 0755)
		if err2 != nil {
			src.Close()
			fail(fmt.Sprintf("Failed to write binary (check permissions): %v", err2))
			os.Exit(1)
		}
		io.Copy(dst, src)
		src.Close()
		dst.Close()
	}

	// On macOS, remove quarantine attribute (ignore errors)
	if runtime.GOOS == "darwin" {
		exec.Command("xattr", "-d", "com.apple.quarantine", exePath).Run()
	}

	ok(fmt.Sprintf("Updated to %s", targetVersion))
	fmt.Println()
}

func fetchLatestRelease() (string, error) {
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

// cliUpdateStageDir returns a private (0700) directory under the user's
// cache root in which to stage CLI update downloads.
func cliUpdateStageDir() (string, error) {
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

func cliDownloadFile(url, path string) error {
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

func cliVerifyReleaseSignature(binPath, sigPath string) error {
	if releaseSigningPubKey == "" {
		return errors.New("release signing public key is not configured in this build; this CLI cannot verify updates (see docs/signing.md)")
	}
	pub, err := hex.DecodeString(releaseSigningPubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("malformed release signing public key")
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("read signature: %w", err)
	}
	for len(sig) > 0 && (sig[len(sig)-1] == '\n' || sig[len(sig)-1] == '\r') {
		sig = sig[:len(sig)-1]
	}
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
