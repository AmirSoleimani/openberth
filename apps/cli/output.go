package main

import (
	"fmt"
	"os"
)

// ── Colors ──────────────────────────────────────────────────────────

const (
	cReset  = "\033[0m"
	cGreen  = "\033[32m"
	cRed    = "\033[31m"
	cYellow = "\033[33m"
	cCyan   = "\033[36m"
	cDim    = "\033[2m"
	cBold   = "\033[1m"
)

func ok(msg string)   { fmt.Printf("  %s✓%s %s\n", cGreen, cReset, msg) }
func fail(msg string) { fmt.Fprintf(os.Stderr, "  %s✗%s %s\n", cRed, cReset, msg) }
func info(msg string) { fmt.Printf("  %s›%s %s\n", cCyan, cReset, msg) }
func warn(msg string) { fmt.Printf("  %s⚠%s %s\n", cYellow, cReset, msg) }
func spin(msg string) { fmt.Printf("  %s⟳%s %s...", cYellow, cReset, msg) }
func done()           { fmt.Println(" done") }
