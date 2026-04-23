package main

import (
	"fmt"
	"os"
	"time"
)

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

func cmdRestore() {
	if len(os.Args) < 3 {
		fail("Usage: berth restore <backup-file> [--passphrase <pass>] [--legacy-unencrypted]")
		os.Exit(1)
	}
	backupFile := os.Args[2]

	if _, err := os.Stat(backupFile); err != nil {
		fail("File not found: " + backupFile)
		os.Exit(1)
	}

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
