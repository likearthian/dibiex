package main

import (
	_ "embed"
	"encoding/json"

	"dibiex/internal/app"
)

//go:embed mygo.json
var appManifest []byte

func main() {
	var manifest struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(appManifest, &manifest)
	app.Run(manifest.Version)
}
