package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/desktop/internal/version"
	"github.com/xswt442-cmd/muxshift/desktop/internal/win"
)

const latestReleaseURL = "https://api.github.com/repos/xswt442-cmd/muxshift/releases/latest"

// updateView is the About page's snapshot of the last check. The check runs
// off the drawing thread; the mutex keeps the page from reading a half-written
// answer.
type updateView struct {
	busy    bool
	checked bool
	latest  string
	url     string
	err     string
}

// checkForUpdates asks GitHub for the latest published release. It is a manual
// check: a window that has never opened About makes no request, and a failed
// check does not retry by itself.
func (a *appState) checkForUpdates() {
	a.updateMu.Lock()
	if a.updateBusy {
		a.updateMu.Unlock()
		return
	}
	a.updateBusy = true
	a.updateErr = ""
	a.updateMu.Unlock()
	a.invalidate()
	go func() {
		release, err := fetchLatestRelease()
		a.updateMu.Lock()
		a.updateBusy = false
		a.updateChecked = true
		if err != nil {
			a.updateErr = err.Error()
			a.updateLatest = ""
			a.updateURL = ""
		} else {
			a.updateLatest = release.TagName
			a.updateURL = release.HTMLURL
			a.updateErr = ""
		}
		a.updateMu.Unlock()
		a.invalidate()
	}()
}

func (a *appState) updateSnapshot() updateView {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	return updateView{
		busy:    a.updateBusy,
		checked: a.updateChecked,
		latest:  a.updateLatest,
		url:     a.updateURL,
		err:     a.updateErr,
	}
}

type latestRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

func fetchLatestRelease() (latestRelease, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return latestRelease{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "MuxShift/"+version.BuildVersion)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return latestRelease{}, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return latestRelease{}, fmt.Errorf("GitHub answered %s", response.Status)
	}
	var release latestRelease
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return latestRelease{}, fmt.Errorf("GitHub sent an unreadable answer: %w", err)
	}
	if release.TagName == "" {
		return latestRelease{}, fmt.Errorf("GitHub did not name a release")
	}
	return release, nil
}

// compareVersions compares x.y.z strings. A development build (0.0.0-dev) is
// older than every release; a malformed value is not ordered.
func compareVersions(current, latest string) (int, error) {
	parse := func(value string) ([3]int, error) {
		var out [3]int
		value = strings.TrimSpace(strings.TrimPrefix(value, "v"))
		value = strings.SplitN(value, "+", 2)[0]
		value = strings.SplitN(value, "-", 2)[0]
		parts := strings.Split(value, ".")
		if len(parts) != 3 {
			return out, fmt.Errorf("%q is not x.y.z", value)
		}
		for i, part := range parts {
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 {
				return out, fmt.Errorf("%q is not x.y.z", value)
			}
			out[i] = n
		}
		return out, nil
	}
	a, err := parse(current)
	if err != nil {
		return 0, err
	}
	b, err := parse(latest)
	if err != nil {
		return 0, err
	}
	for i := range a {
		if a[i] < b[i] {
			return -1, nil
		}
		if a[i] > b[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func (a *appState) aboutPage(c *ui.Context) {
	osName, memory := win.SystemInfo()
	rows := []struct {
		label string
		value string
	}{
		{a.tr("Version", "版本"), version.Line()},
		{a.tr("Built with", "构建环境"), version.BuildLine()},
		{a.tr("Operating system", "操作系统"), osName + " · " + runtime.GOARCH},
		{a.tr("Logical processors", "逻辑处理器"), strconv.Itoa(runtime.NumCPU())},
		{a.tr("Physical memory", "物理内存"), bytesLine(int64(memory))},
		{a.tr("Settings file", "设置文件"), a.settingsPath},
	}
	for _, row := range rows {
		ui.Row(c).WidthPercent(100).Gap(spaceMd).Children(func() {
			ui.Text(c, row.label).Width(150).FontSize(textMicro).TextColor(colorDim)
			ui.Text(c, row.value).FontSize(textSmall).TextColor(colorText).Wrap()
		})
	}
	ui.Box(c).WidthPercent(100).Height(1).Background(colorLineSoft)
	view := a.updateSnapshot()
	ui.Row(c).Gap(spaceSm).AlignItems(ui.Center).Children(func() {
		label := a.tr("Check for updates", "检查更新")
		if view.busy {
			label = a.tr("Checking…", "检查中…")
		}
		button := ui.Button(c.Key("about-update"), label).Height(30)
		if view.busy {
			button = button.Disabled(true)
		}
		if button.Clicked() {
			a.checkForUpdates()
		}
		switch {
		case view.err != "":
			ui.Text(c, view.err).FontSize(textSmall).TextColor(colorBad).Wrap()
		case !view.checked:
			ui.Text(c, a.tr("Not checked yet.", "还没有检查。")).FontSize(textSmall).TextColor(colorTextDim)
		case versionsEqual(view.latest, version.BuildVersion):
			ui.Text(c, a.tr("This is the latest release.", "已经是最新版本。")).FontSize(textSmall).TextColor(colorGood)
		default:
			ui.Text(c, a.tr("Latest release: ", "最新版本：")+view.latest).FontSize(textSmall).TextColor(colorText)
		}
	})
	if view.url != "" && !versionsEqual(view.latest, version.BuildVersion) {
		ui.Button(c.Key("about-open-release"), a.tr("Open release page", "打开发布页")).Height(30).Width(180).
			OnClick(func() {
				opener := a.openURL
				if opener == nil {
					opener = a.contextOpenURL
				}
				if opener != nil {
					opener(view.url, func(err error) {
						if err != nil {
							a.lastErr = err
							a.invalidate()
						}
					})
				}
			})
	}
}

func versionsEqual(a, b string) bool {
	n, err := compareVersions(a, b)
	return err == nil && n == 0
}
