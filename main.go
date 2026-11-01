// Command herdr-mygo is a desktop client for herdr-core, built with MyGo.
package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:    "Herdr",
			Width:    1200,
			Height:   780,
			StateKey: "main",
			Content: ui.View(func(c *ui.Context) {
				ui.Text(c, "Herdr")
			}),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
