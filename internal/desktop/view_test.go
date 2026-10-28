package desktop

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/ca-x/cognate/assets/branding"
	"github.com/egoist/mygo/ui"
)

func TestNativePagesLightDarkAndNarrow(test *testing.T) {
	for _, dark := range []bool{false, true} {
		for page, title := range []string{"搜索", "保存订阅", "预览文件", "本地账号登录"} {
			test.Run(fmt.Sprintf("dark-%t-page-%d", dark, page), func(test *testing.T) {
				model, _ := modelFixture(test, &Service{Home: test.TempDir()})
				model.Page = page
				logo, err := ui.DecodeBitmap(clientlogoassets.ClientPNG)
				if err != nil {
					test.Fatal(err)
				}
				model.Logo = logo
				tester := ui.NewTester(model.View, 640, 600)
				tester.SetDark(dark)
				if !tester.HasText(title) {
					test.Fatal("native page action missing")
				}
				if directory := os.Getenv("DESKTOP_EVIDENCE_DIR"); directory != "" {
					file, err := os.Create(filepath.Join(directory, fmt.Sprintf("headless-dark-%t-page-%d.png", dark, page)))
					if err != nil {
						test.Fatal(err)
					}
					if err := png.Encode(file, tester.Image()); err != nil {
						_ = file.Close()
						test.Fatal(err)
					}
					if err := file.Close(); err != nil {
						test.Fatal(err)
					}
				}
			})
		}
	}
}
