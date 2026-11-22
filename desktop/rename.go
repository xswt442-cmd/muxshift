// The rename dialog gives a scene or a source the person's own display name.
// It is one field and two buttons, opened from the pencil on a row; committing
// writes the name through Core, which trims it and treats an empty value as
// "use the automatic name".
package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/xswt442-cmd/muxshift/core"
)

// renameTarget says which kind of object the rename dialog edits.
type renameTarget int

const (
	renameNone renameTarget = iota
	renameScene
	renameSource
)

// openRenameScene opens the dialog on one scene, starting from its current name.
func (a *appState) openRenameScene(id core.SceneID) {
	name := ""
	if scene, err := a.session.Scene(id); err == nil {
		name = scene.Name
	}
	a.renameOpen = true
	a.renameKind = renameScene
	a.renameID = string(id)
	a.renameText = name
}

// openRenameSource opens the dialog on one source, starting from its current
// user-set name (empty when it has none).
func (a *appState) openRenameSource(id core.SourceID) {
	name := ""
	if src, err := a.session.Source(id); err == nil {
		name = src.Name
	}
	a.renameOpen = true
	a.renameKind = renameSource
	a.renameID = string(id)
	a.renameText = name
}

// closeRename dismisses the dialog without applying anything.
func (a *appState) closeRename() {
	a.renameOpen = false
	a.renameKind = renameNone
	a.renameID = ""
	a.renameText = ""
}

// refreshItemSource copies a renamed source back into its card, so the list and
// the mixer show the new name without waiting for the next reload.
func (a *appState) refreshItemSource(id core.SourceID) {
	src, err := a.session.Source(id)
	if err != nil {
		return
	}
	if it := a.itemOf(id); it != nil {
		it.source = src
	}
}

// confirmRename applies the field. An empty field clears the name, so the
// display falls back to the automatic label; Core does the trimming and capping.
func (a *appState) confirmRename() {
	var err error
	switch a.renameKind {
	case renameScene:
		err = a.session.RenameScene(core.SceneID(a.renameID), a.renameText)
	case renameSource:
		id := core.SourceID(a.renameID)
		if err = a.session.RenameSource(id, a.renameText); err == nil {
			a.refreshItemSource(id)
		}
	default:
		a.closeRename()
		return
	}
	if err != nil {
		a.lastErr = safeOpenError(err)
		a.invalidate()
		return
	}
	a.closeRename()
	a.apply()
	a.saveSession()
	a.invalidate()
}

// renameDialog is the modal itself, on the same primitives as addSourceDialog.
func (a *appState) renameDialog(c *ui.Context) {
	ui.DialogBase(c, &a.renameOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		panel = panel.Background(colorPanel2).Border(1, colorLine).Radius(radiusLg).
			Padding(spaceXl).Gap(spaceMd).Shadow(0, 20, 48, 0, shadowDialog)
		panel.Children(func() { a.renamePanel(c) })
	})
}

// renamePanel is the dialog's body.
func (a *appState) renamePanel(c *ui.Context) {
	title := a.tr("Rename scene", "重命名场景")
	if a.renameKind == renameSource {
		title = a.tr("Rename source", "重命名来源")
	}
	submit := func() { a.confirmRename() }
	ui.Column(c).Gap(spaceSm).MinWidth(360).MaxWidth(420).Children(func() {
		ui.Text(c, title).FontSize(textTitle).FontWeight(600).TextColor(colorText)
		field := a.field(c, &a.renameText).WidthPercent(100).
			Placeholder(a.tr("leave empty for the automatic name", "留空则使用自动名称")).
			MaxLength(200)
		field.OnTextKey(func(k ui.TextKey) bool {
			if k.Key != ui.KeyEnter || k.Mods != 0 {
				return false
			}
			submit()
			return true
		})
		ui.Text(c, a.tr("Leave it empty to use the automatic name.",
			"留空将使用自动名称。")).
			FontSize(textMicro).FontWeight(400).TextColor(colorDim)
		ui.Row(c).Gap(spaceSm).WidthPercent(100).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1)
			a.secondaryButton(c, "rename-cancel", a.tr("Cancel", "取消"), 88, true, func() {
				a.closeRename()
			})
			a.primaryButton(c, "rename-confirm", a.tr("OK", "确定"), 88, true, func() {
				submit()
			})
		})
	})
}
