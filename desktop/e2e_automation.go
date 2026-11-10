//go:build e2e

// This file imports MyGo's automation plugin into the e2e build only, so a
// test runner can deliver real key events to the window even when it cannot
// take the foreground. The normal build leaves it out.
package main

import _ "github.com/egoist/mygo/plugins/automation"
