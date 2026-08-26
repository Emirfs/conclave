package main

import (
	"embed"
	"flag"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

const defaultAddress = "127.0.0.1:7331"

func main() {
	address := flag.String("address", defaultAddress, "daemon address")
	flag.Parse()

	app := NewApp(*address)
	err := wails.Run(&options.App{
		Title:     "Conclave",
		Width:     1440,
		Height:    900,
		MinWidth:  960,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// Matches --surface in the frontend theme so resizing never flashes white.
		BackgroundColour: &options.RGBA{R: 0x0B, G: 0x0D, B: 0x12, A: 1},
		Frameless:        true,
		OnStartup:        app.startup,
		Bind:             []any{app},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "conclave-desktop:", err)
		os.Exit(1)
	}
}
