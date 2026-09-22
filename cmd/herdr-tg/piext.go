package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	piextension "github.com/spancerxing/herdr-telegram-bridge/pi-extension"
)

// installPiExtension writes the herdr:blocked emitter into pi's global
// extensions directory.
//
// This edits the user's pi configuration, so it is a separate explicit
// command rather than something the daemon does on startup, and it refuses to
// clobber a file it does not own.
func installPiExtension(dryRun, force bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	dir := filepath.Dir(piExtensionTarget(home))
	target := piExtensionTarget(home)

	if err := checkPiPresent(home); err != nil {
		return err
	}

	existing, readErr := os.ReadFile(target)
	existingText := string(existing)
	switch {
	case readErr == nil && existingText == piextension.BlockedEmitter:
		fmt.Printf("already up to date: %s\n", target)
		return nil
	case readErr == nil && !strings.Contains(existingText, piextension.ManagedMarker) && !force:
		return fmt.Errorf(
			"%s already exists and was not written by this plugin\n"+
				"  refusing to overwrite it; move it aside, or re-run with --force",
			target)
	case readErr == nil:
		fmt.Printf("will replace %s (managed, changed)\n", target)
	case errors.Is(readErr, os.ErrNotExist):
		fmt.Printf("will create %s\n", target)
	default:
		return fmt.Errorf("read %s: %w", target, readErr)
	}

	if dryRun {
		fmt.Println("dry run: nothing written")
		return nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	// Write via a temporary file in the same directory so pi can never load a
	// half-written extension, which would otherwise be a real risk on the
	// /reload path these files are designed for.
	tmp, err := os.CreateTemp(dir, ".herdr-blocked-emitter-*.ts")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.WriteString(piextension.BlockedEmitter); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("install %s: %w", target, err)
	}

	fmt.Printf("installed %s\n", target)
	fmt.Println()
	fmt.Println("pi loads extensions at startup; run /reload in a running session, or start a new one.")
	fmt.Println("Verify with: herdr-tg integrations")
	return nil
}

// checkPiPresent fails early with a clear message when pi has never run on
// this machine, rather than creating a directory tree pi will not look at.
func checkPiPresent(home string) error {
	if _, err := os.Stat(filepath.Join(home, ".pi")); err != nil {
		return fmt.Errorf("pi does not look installed (no %s); install pi first, or run it once", filepath.Join(home, ".pi"))
	}
	return nil
}

func piExtensionTarget(home string) string {
	return filepath.Join(home, filepath.FromSlash(piextension.ExtensionDir), piextension.FileName)
}

// piExtensionStatus reports whether the emitter and Herdr's consumer are both
// in place, which is what makes blocked work on pi. Both are required and the
// failure mode when only one is present is silent, so it is worth saying
// explicitly.
func piExtensionStatus(home string) (emitter bool, emitterPath string) {
	target := piExtensionTarget(home)
	b, err := os.ReadFile(target)
	if err != nil {
		return false, target
	}
	return strings.Contains(string(b), piextension.ManagedMarker), target
}
