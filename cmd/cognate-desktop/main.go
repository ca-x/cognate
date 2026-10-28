package main

import (
	"context"
	"flag"
	"log"
	"os"
	"sync"
	"time"

	"github.com/ca-x/cognate/assets/branding"

	"github.com/ca-x/cognate/internal/desktop"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	smoke := flag.Duration("smoke", 0, "capture the native content and quit after this duration; does not perform installation")
	capture := flag.String("capture", "", "native content PNG output for smoke")
	flag.Parse()
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var window *mygo.Window
	model := desktop.NewModel(&desktop.Service{Home: home}, func(commit func()) { window.Update(commit) })
	logo, err := ui.DecodeBitmap(clientlogoassets.ClientPNG)
	if err != nil {
		return err
	}
	model.Logo = logo
	var workers sync.WaitGroup
	var smokeErr error
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	closeWindow := func() { cancel(); model.Close() }
	mygo.App.OnQuit(closeWindow)
	mygo.App.WhenReady(func() {
		window = mygo.NewWindow(mygo.WindowOptions{Title: "Cognate", Width: 920, Height: 760, Content: ui.View(model.View)})
		window.OnClosed(closeWindow)
		log.Print("NATIVE_WINDOW_CREATED")
		if *smoke > 0 {
			workers.Go(func() {
				timer := time.NewTimer(*smoke)
				defer timer.Stop()
				select {
				case <-life.Done():
					return
				case <-timer.C:
				}
				if *capture != "" {
					type captureResult struct {
						image []byte
						err   error
					}
					ready := make(chan captureResult, 1)
					window.Update(func() {
						if life.Err() != nil {
							return
						}
						image, err := window.CapturePage()
						ready <- captureResult{image: image, err: err}
					})
					var result captureResult
					select {
					case <-life.Done():
						return
					case result = <-ready:
					}
					smokeErr = result.err
					if smokeErr == nil {
						smokeErr = os.WriteFile(*capture, result.image, 0600)
					}
					if smokeErr == nil {
						log.Print("NATIVE_CAPTURE_OK")
					}
				}
				mygo.App.Quit()
			})
		}
	})
	err = mygo.App.Run()
	closeWindow()
	model.Wait()
	workers.Wait()
	if err != nil {
		return err
	}
	return smokeErr
}
