package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// firstPositional returns the first non-flag argument after the
// subcommand verb (i.e. arg[2] onwards in os.Args), or "" if all
// remaining args are flags or flag values. Flags are recognised by
// the leading "--"; flag values immediately follow their flag name
// and are skipped.
func firstPositional() string {
	for i := 2; i < len(os.Args); i++ {
		a := os.Args[i]
		if strings.HasPrefix(a, "--") {
			i++ // skip the flag's value (cheap heuristic; matches getFlag)
			continue
		}
		return a
	}
	return ""
}

// backupPassphrase returns the passphrase from --passphrase, else the
// BERTH_BACKUP_PASSPHRASE environment variable. Passing secrets on the
// command line leaks them to shell history; we document BERTH_BACKUP_PASSPHRASE
// as the recommended channel.
func backupPassphrase() string {
	if p := getFlag("passphrase", ""); p != "" {
		return p
	}
	return os.Getenv("BERTH_BACKUP_PASSPHRASE")
}

func cmdBackup() {
	deployID := firstPositional()
	if deployID != "" {
		cmdBackupDeployment(deployID)
		return
	}

	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Backup%s\n\n", cBold, cReset)

	output := getFlag("output", fmt.Sprintf("openberth-backup-%s.obbk", time.Now().Format("2006-01-02")))

	pass := backupPassphrase()
	if pass == "" {
		fail("Backup passphrase required. Use --passphrase or set BERTH_BACKUP_PASSPHRASE.")
		os.Exit(1)
	}
	if len(pass) < 12 {
		fail("Backup passphrase must be at least 12 characters.")
		os.Exit(1)
	}

	spin("Downloading encrypted backup")
	client, err := NewAPIClient()
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}

	size, err := client.PostDownload("/api/admin/backup", map[string]string{"passphrase": pass}, output)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	ok(fmt.Sprintf("Encrypted backup saved: %s%s%s (%s)", cBold, output, cReset, formatSize(size)))
	warn("Store the passphrase separately — the backup cannot be decrypted without it.")
	fmt.Println()
}

// cmdBackupDeployment is the per-deployment branch of `berth backup <id>`.
// Same passphrase / env-var / output-flag conventions; different endpoint
// and a deployment-specific default filename.
func cmdBackupDeployment(deployID string) {
	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Deployment Backup%s\n\n", cBold, cReset)

	output := getFlag("output", fmt.Sprintf("ob-deploy-%s-%s.obdp", deployID, time.Now().Format("2006-01-02")))

	pass := backupPassphrase()
	if pass == "" {
		fail("Backup passphrase required. Use --passphrase or set BERTH_BACKUP_PASSPHRASE.")
		os.Exit(1)
	}
	if len(pass) < 12 {
		fail("Backup passphrase must be at least 12 characters.")
		os.Exit(1)
	}

	spin("Downloading encrypted deployment backup")
	client, err := NewAPIClient()
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}

	size, err := client.PostDownload("/api/admin/deployments/"+deployID+"/backup", map[string]string{"passphrase": pass}, output)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	ok(fmt.Sprintf("Encrypted deployment backup saved: %s%s%s (%s)", cBold, output, cReset, formatSize(size)))
	info(fmt.Sprintf("Restore with: berth restore %s --passphrase ...", output))
	warn("Store the passphrase separately — the backup cannot be decrypted without it.")
	fmt.Println()
}

func cmdRestore() {
	if len(os.Args) < 3 {
		fail("Usage: berth restore <backup-file> [--passphrase <pass>] [--legacy-unencrypted] [--owner <user>] [--skip-missing-secrets]")
		os.Exit(1)
	}
	backupFile := os.Args[2]

	if _, err := os.Stat(backupFile); err != nil {
		fail("File not found: " + backupFile)
		os.Exit(1)
	}

	// Peek the first 6 bytes to dispatch on archive magic.
	magic, err := readBackupMagic(backupFile)
	if err != nil {
		fail("Failed to read backup file: " + err.Error())
		os.Exit(1)
	}
	if magic == "OBDP01" {
		cmdRestoreDeployment(backupFile)
		return
	}
	// Fall through to server-wide restore for OBBK01 and any
	// pre-encryption-era unencrypted tarballs.

	pass := backupPassphrase()
	legacy := getFlag("legacy-unencrypted", "") != ""

	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Restore%s\n\n", cBold, cReset)

	fields := map[string]string{}
	if pass != "" {
		fields["passphrase"] = pass
	}
	if legacy {
		fields["legacyUnencrypted"] = "true"
		warn("Accepting legacy unencrypted backup format.")
	}

	spin("Uploading backup and restoring")
	client, err := NewAPIClient()
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}

	result, err := client.UploadFileWithFields("/api/admin/restore", backupFile, "backup", fields)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	msg, _ := result["message"].(string)
	users, _ := result["users"].(float64)
	deploys, _ := result["deployments"].(float64)
	rebuilding, _ := result["rebuilding"].(float64)

	ok("Backup restored successfully.")
	info(fmt.Sprintf("Users: %d", int(users)))
	info(fmt.Sprintf("Deployments: %d", int(deploys)))
	if rebuilding > 0 {
		info(fmt.Sprintf("Rebuilding: %d deployment(s) in background", int(rebuilding)))
		warn("TLS certificates may take a few minutes to provision — expect brief SSL errors until then.")
	}
	_ = msg
	fmt.Println()
}

// readBackupMagic returns the first 6 bytes of the file as a string.
// Used to dispatch `berth restore` to the right endpoint without
// downloading or decrypting anything.
func readBackupMagic(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 6)
	n, _ := f.Read(buf)
	return string(buf[:n]), nil
}

// cmdRestoreDeployment is the per-deployment branch of `berth restore <file>`,
// triggered when the archive's magic is OBDP01.
func cmdRestoreDeployment(backupFile string) {
	pass := backupPassphrase()
	owner := getFlag("owner", "")
	skipMissing := getFlag("skip-missing-secrets", "") != ""

	fmt.Println()
	fmt.Printf("  %s⚓ OpenBerth Deployment Restore%s\n\n", cBold, cReset)

	fields := map[string]string{}
	if pass != "" {
		fields["passphrase"] = pass
	}
	if owner != "" {
		fields["owner"] = owner
	}
	if skipMissing {
		fields["skipMissingSecrets"] = "true"
		warn("Will proceed even if referenced secrets are missing on this server.")
	}

	spin("Uploading deployment backup and restoring")
	client, err := NewAPIClient()
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}

	result, err := client.UploadFileWithFields("/api/admin/deployments/restore", backupFile, "backup", fields)
	if err != nil {
		done()
		fail(err.Error())
		os.Exit(1)
	}
	done()

	id, _ := result["id"].(string)
	subdomain, _ := result["subdomain"].(string)
	status, _ := result["status"].(string)
	originalID, _ := result["originalId"].(string)
	originalSubdomain, _ := result["originalSubdomain"].(string)

	ok("Deployment restore in progress.")
	info(fmt.Sprintf("New ID:        %s%s%s", cBold, id, cReset))
	info(fmt.Sprintf("New subdomain: %s%s%s", cBold, subdomain, cReset))
	info(fmt.Sprintf("Status:        %s", status))
	info(fmt.Sprintf("Original:      %s (%s)", originalSubdomain, originalID))

	if missing, ok := result["secretsMissing"].([]any); ok && len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for _, n := range missing {
			if s, _ := n.(string); s != "" {
				names = append(names, s)
			}
		}
		if len(names) > 0 {
			warn("Missing secrets on target: " + strings.Join(names, ", "))
		}
	}

	warn("Build is running in the background. Check 'berth status " + id + "' or 'berth logs " + id + "'.")
	fmt.Println()
}
