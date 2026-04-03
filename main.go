// @title          kansou API
// @version         1.0
// @description     Personal anime/manga scoring tool with AniList integration.
// @contact.name    kansou
// @license.name    MIT
// @host            localhost:8080
// @BasePath        /

// kansou is a personal anime/manga scoring CLI and REST server.
// It fetches media metadata from AniList, guides an interactive scoring session,
// applies a weighted genre-adjusted formula, and publishes the final score.
//
// Usage:
//
//	kansou score add "Frieren"    # start a scoring session (includes publish prompt)
//	kansou media find "Mushishi"  # look up media without scoring
//	kansou serve                  # start the REST server
package main

import (
	"github.com/kondanta/kansou/cmd"
	_ "github.com/kondanta/kansou/docs/swagger" // registers Swagger spec via init()
)

func main() {
	cmd.Execute()
}
