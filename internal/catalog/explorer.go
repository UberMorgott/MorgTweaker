package catalog

import (
	"golang.org/x/sys/windows/registry"

	"morgtweaker/internal/action"
	"morgtweaker/internal/core"
)

// explorer is the "Explorer" category.
var explorer = []core.Tweak{
	{
		ID: "explorer.show_file_ext", Category: "explorer",
		Name: core.I18n{RU: "Показывать расширения файлов", EN: "Show file extensions"},
		Desc: core.I18n{RU: "Всегда показывать расширения известных типов файлов в Проводнике.", EN: "Always show known file-type extensions in Explorer."},
		Elevation: core.ElevUser,
		Actions: []core.Action{action.RegSet{
			Root:  registry.CURRENT_USER,
			Path:  `Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced`,
			Value: "HideFileExt", Kind: action.KindDword, On: uint64(0), Off: uint64(1), Elev: core.ElevUser,
		}},
	},
}
