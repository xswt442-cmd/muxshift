package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/xswt442-cmd/muxshift/core"
)

// Source memory is a preference, not an authorisation.
//
// It remembers two things about an address a person worked with: the level they
// set for its sound, and whether they muted that source. It does not remember
// where the play head was, which offset a track was given, or whether an
// unaligned source was allowed to be heard: those describe this run's timing,
// and a new run has to measure them again. It also does not open anything: the
// command line still says which sources a window starts with.
//
// The identity is the normalised address, never the session's source-1: that
// changes every run. Two rules follow from "preference, not authorisation":
// addresses that may carry a secret (credentials, a query string, a fragment)
// are refused rather than cleaned, because cleaning produces a different
// address that may not open at all; and a test-only address that cannot be
// normalised is simply not remembered.

const (
	// maxRememberedSources bounds the file. The bound is refused, not enforced
	// by dropping somebody's older preferences.
	maxRememberedSources = 50
	// maxIdentityLength is the longest address worth remembering. Anything
	// longer is refused rather than truncated into a different address.
	maxIdentityLength = 2048
)

// sourceMemoryEntry is one remembered address. The order of the entries is
// "most recently saved first"; a read that only matches an address does not
// move an entry, so the order never claims a play order.
type sourceMemoryEntry struct {
	Address string  `json:"address"`
	Volume  float64 `json:"volume"`
	Muted   bool    `json:"muted"`
}

// sourceMemory is the settings block for those entries.
//
// On is the master switch: while it is off, entries are neither applied nor
// written, but they stay in the file until somebody presses Clear. Remember
// decides whether a newly added source updates an entry; it is separate from On
// because "use what you already know" and "keep learning new addresses" are
// different choices.
type sourceMemory struct {
	On       bool                `json:"on"`
	Remember bool                `json:"remember"`
	Entries  []sourceMemoryEntry `json:"entries"`
}

// clone copies the entries, so a draft and the applied settings never share a
// backing array.
func (m sourceMemory) clone() sourceMemory {
	next := m
	next.Entries = append([]sourceMemoryEntry(nil), m.Entries...)
	return next
}

// indexOf finds the entry for an identity, or -1.
func (m sourceMemory) indexOf(identity string) int {
	for i, entry := range m.Entries {
		if entry.Address == identity {
			return i
		}
	}
	return -1
}

// lookup reports the remembered level and mute for an identity.
func (m sourceMemory) lookup(identity string) (volume float64, muted bool, ok bool) {
	if i := m.indexOf(identity); i >= 0 {
		return m.Entries[i].Volume, m.Entries[i].Muted, true
	}
	return 0, false, false
}

// set records a level and mute for an identity, newest first. The refusal is a
// sentence for the page, not an error: not remembering an address is normal.
func (m *sourceMemory) set(identity string, volume float64, muted bool) (bool, string) {
	if i := m.indexOf(identity); i >= 0 {
		if m.Entries[i].Volume == volume && m.Entries[i].Muted == muted {
			return false, ""
		}
		m.Entries = append(m.Entries[:i], m.Entries[i+1:]...)
		m.Entries = append([]sourceMemoryEntry{{Address: identity, Volume: volume, Muted: muted}}, m.Entries...)
		return true, ""
	}
	if len(m.Entries) >= maxRememberedSources {
		return false, fmt.Sprintf("%d addresses are already remembered; clear some before adding another", len(m.Entries))
	}
	m.Entries = append([]sourceMemoryEntry{{Address: identity, Volume: volume, Muted: muted}}, m.Entries...)
	return true, ""
}

// clear forgets every entry and reports how many were forgotten.
func (m *sourceMemory) clear() int {
	n := len(m.Entries)
	m.Entries = nil
	return n
}

// normalizeSourceIdentity turns an address a person typed into the identity the
// settings file stores, or explains why it will not be remembered.
//
// Only two shapes are accepted: an http(s) address and an absolute local path.
// A `file:` URL is refused because the player's own open path does not accept
// one either; remembering it would store something this build cannot open.
func normalizeSourceIdentity(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("the address is empty")
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "file:") {
		return "", errors.New("file: addresses are not remembered yet; add the local path instead")
	}
	if strings.Contains(trimmed, "://") {
		return normalizeHTTPIdentity(trimmed)
	}
	return normalizeLocalIdentity(trimmed)
}

// normalizeHTTPIdentity keeps an http(s) address's path exactly as typed.
//
// The scheme, the host's case and a default port are spelling, so they are
// normalised. The path is not: servers, proxies and signature schemes are
// allowed to treat /a//b, /a/b and an encoded slash as different resources, and
// folding them here would claim two addresses are one. A false negative (two
// identities for one resource) only costs a preference; a false positive would
// apply one source's level to another's sound.
func normalizeHTTPIdentity(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("this address cannot be read: %v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https":
	default:
		return "", fmt.Errorf("%q addresses are not remembered; use an http(s) address or a local file", u.Scheme)
	}
	if u.User != nil {
		return "", fmt.Errorf("%w: this address carries credentials, so it is not written to the settings file", core.ErrCredentialsInURI)
	}
	// ForceQuery catches a bare trailing "?": it has no query to read, but it
	// still says the address was written with one, so the same refusal applies.
	if u.RawQuery != "" || u.ForceQuery {
		return "", errors.New("this address has a query string, which may carry a signature; it is not written to the settings file")
	}
	if u.Fragment != "" {
		return "", errors.New("this address has a fragment, so it is not written to the settings file")
	}
	if u.Host == "" {
		return "", errors.New("this address has no host")
	}
	host := strings.ToLower(u.Host)
	if name, port, err := net.SplitHostPort(host); err == nil {
		if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
			host = name
		}
	}
	identity := scheme + "://" + host + u.EscapedPath()
	if len(identity) > maxIdentityLength {
		return "", fmt.Errorf("the address is longer than %d characters, so it is not remembered", maxIdentityLength)
	}
	return identity, nil
}

// normalizeLocalIdentity makes one spelling of a path. Symbolic links are not
// resolved: two paths to one file are two identities, which costs a preference
// and never applies one file's level to another file.
func normalizeLocalIdentity(raw string) (string, error) {
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("only absolute local paths are remembered, and %q is relative", raw)
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("this path cannot be read: %v", err)
	}
	fixed := filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		fixed = strings.ToLower(fixed)
	}
	if len(fixed) > maxIdentityLength {
		return "", fmt.Errorf("the path is longer than %d characters, so it is not remembered", maxIdentityLength)
	}
	return fixed, nil
}

// identityOf is the identity for an item's address, or "" when the address
// cannot be remembered. Callers treat "" as "no preference".
func identityOf(uri string) string {
	identity, err := normalizeSourceIdentity(uri)
	if err != nil {
		return ""
	}
	return identity
}

// identityDigest names an identity in a log line without printing it. The first
// characters of an http address are always "https://", so a prefix would say
// nothing; a digest prefix distinguishes two entries without revealing either.
func identityDigest(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])[:8]
}

// memoryDigest names the whole set, so two different remembered sets do not log
// the same summary.
func memoryDigest(m sourceMemory) string {
	parts := make([]string, 0, len(m.Entries))
	for _, entry := range m.Entries {
		parts = append(parts, entry.Address)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:8]
}
