// The scripted checks: each drives the players and the session through their
// own methods and captures the window, so what is recorded is the players'
// state rather than a click no test can make.
package main

import (
	"github.com/egoist/mygo"
)

// scenario runs a scripted check against the window and its sources.
type scenario func(window *mygo.Window, a *appState, dir string)

// scenarios are the checks -scenario names.
var scenarios = map[string]scenario{
	"single":            runSingle,
	"reopen":            runReopen,
	"reopen-strict":     runReopenStrict,
	"handover-fail":     runHandoverFail,
	"reload-rapid":      runReloadRapid,
	"pair":              runPair,
	"replay-swap":       runReplaySwap,
	"switch":            runSwitch,
	"rapid":             runRapid,
	"retry":             runRetry,
	"scene":             runScene,
	"four":              runFour,
	"audio":             runAudio,
	"sync":              runSync,
	"syncgroup":         runSyncGroup,
	"syncmap":           runSyncMap,
	"syncfail":          runSyncFail,
	"dvr":               runDVR,
	"replayprobe":       runReplayProbe,
	"align-probe":       runAlignProbe,
	"align-async":       runAlignAsync,
	"commands":          runCommands,
	"webview":           runWebView,
	"workspaces":        runWorkspaces,
	"workbench":         runWorkbench,
	"matrix":            runMatrix,
	"lifecycle":         runLifecycle,
	"sources":           runSources,
	"settings":          runSettings,
	"settings-restart":  runSettingsRestart,
	"keys":              runKeys,
	"narrow":            runNarrow,
	"sizes":             runSizes,
	"sizes-empty":       runSizesEmpty,
	"dpi":               runDPI,
	"windows":           runWindows,
	"pages":             runPages,
	"focus":             runFocus,
	"realdpi":           runRealDPI,
	"workspace":         runWorkspace,
	"workspace-restore": runWorkspaceRestore,
	"hidden":            runHidden,
	"contract":          runContract,
	"lease":             runLease,
	"sweep":             runSweep,
	"replay":            runReplay,
	"swap":              runSwap,
	"swap-two":          runSwapTwo,
	"common":            runCommon,
	"midjoin":           runMidJoin,
	"frames":            runFrames,
	"faults":            runFaults,
	"final":             runFinal,
	"seektxn":           runSeekTxn,
	"interleave":        runInterleave,
	"replayui":          runReplayUI,
	"timeline":          runTimeline,
	"alignreport":       runAlignReport,
	"mixer":             runMixer,
	"mutehold":          runMuteHold,
	"clock":             runClock,
	"playthrough":       runPlayThrough,
}
