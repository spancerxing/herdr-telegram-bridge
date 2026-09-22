// Package piextension carries the pi extension this plugin installs into the
// user's pi configuration.
//
// The extension lives beside this file rather than being generated from a Go
// string so that it can be read, linted and hot-reloaded like any other pi
// extension, and so `bun build` can check it in CI. It is embedded as well so
// the installer works no matter where the plugin was installed from.
package piextension

import _ "embed"

// ManagedMarker identifies a file this plugin owns. The installer refuses to
// overwrite a file that does not carry it, so a hand-written extension that
// happens to share the name is never silently destroyed.
const ManagedMarker = "@herdr-telegram-multi-managed"

// FileName is the name the file must have in pi's extensions directory for pi
// to load it. The file's own name is not significant to pi (any *.ts in the
// directory is loaded), but a stable name is what makes reinstall idempotent.
const FileName = "herdr-blocked-emitter.ts"

// BlockedEmitter is the source of the extension that republishes pi's
// ui_prompt_start / ui_prompt_end on the herdr:blocked event bus event that
// Herdr's own pi integration listens for.
//
//go:embed herdr-blocked-emitter.ts
var BlockedEmitter string

// ExtensionDir is where pi auto-discovers global extensions, relative to the
// user's home directory (docs/extensions.md, "Extension Locations").
const ExtensionDir = ".pi/agent/extensions"
