package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/egoist/mygo/ui"
)

type blockedBackend struct {
	Backend
	started  chan struct{}
	release  chan struct{}
	canceled chan struct{}
}

type blockedDetails struct {
	Backend
	started, release chan struct{}
	result           *catalogv1.PublicSkill
}

func (backend *blockedDetails) Details(context.Context, string, string) (*catalogv1.PublicSkill, error) {
	close(backend.started)
	<-backend.release
	return backend.result, nil
}

func TestNativeDetailRefreshCannotReuseOldConfirmation(test *testing.T) {
	original := &catalogv1.PublicSkill{Id: "first", Name: "First", ContentHash: strings.Repeat("a", 64), PublicationRevision: 1}
	updated := &catalogv1.PublicSkill{Id: "first", Name: "Changed", ContentHash: strings.Repeat("b", 64), PublicationRevision: 2}
	backend := &blockedDetails{started: make(chan struct{}), release: make(chan struct{}), result: updated}
	model, pump := modelFixture(test, backend)
	model.SetOrigin("https://example.org")
	model.Skills = []*catalogv1.PublicSkill{original}
	model.Selected = 0
	model.Detail = original
	model.Confirm = true
	tester := ui.NewTester(model.View, 920, 760)
	click(test, tester, "查看详情")
	<-backend.started
	accepted := tester.Click("确认下载完整技能内容并写入本地 ~/.agents/skills 与已适配 agent 目录") == nil && model.Confirm
	close(backend.release)
	pump()
	tester.Frame()
	if accepted || model.Confirm {
		test.Fatal("confirmation of old details applied to newly fetched content")
	}
	if model.Detail != updated {
		test.Fatal("updated details were not shown")
	}
}

type blockedLogin struct {
	Backend
	started, release chan struct{}
}

func (backend *blockedLogin) Login(context.Context, string, string, string) (string, error) {
	close(backend.started)
	<-backend.release
	return strings.Repeat("A", 43) + "." + strings.Repeat("B", 43), nil
}

func TestNativeAccountChangeRejectsOldLogin(test *testing.T) {
	backend := &blockedLogin{started: make(chan struct{}), release: make(chan struct{})}
	model, pump := modelFixture(test, backend)
	model.SetOrigin("https://example.org")
	model.Page = 3
	model.Username = "old-owner"
	model.Password = "not-persisted"
	tester := ui.NewTester(model.View, 920, 760)
	click(test, tester, "登录")
	<-backend.started
	click(test, tester, "用户名")
	tester.Type("-new")
	close(backend.release)
	pump()
	tester.Frame()
	if model.session != "" || model.Busy {
		test.Fatal("old owner login repopulated session")
	}
	model.session = "secret-session"
	model.Password = "secret-password"
	model.ShareToken = "secret-share"
	if text := fmt.Sprintf("%v %+v %#v", model, model, model); strings.Contains(text, "secret-") {
		test.Fatal("model debug formatting disclosed credentials")
	}
}

func TestNativeChangingSelectionRevokesConfirmation(test *testing.T) {
	model, pump := modelFixture(test, &Service{Home: test.TempDir()})
	model.SetOrigin("https://example.org")
	model.Skills = []*catalogv1.PublicSkill{{Id: "first", Name: "First"}, {Id: "second", Name: "Second"}}
	model.Selected = 0
	model.Detail = model.Skills[0]
	model.Confirm = true
	tester := ui.NewTester(model.View, 920, 760)
	model.skillList.Handle.Focus()
	tester.Frame()
	tester.Key(0, ui.KeyDown)
	pump()
	if model.Selected != 1 || model.Confirm || model.Detail != nil {
		test.Fatal("native selection retained old installation confirmation")
	}
}

func (backend *blockedBackend) Search(ctx context.Context, _, _, _ string) (*catalogv1.ListSkillsResponse, error) {
	close(backend.started)
	<-backend.release
	if ctx.Err() != nil {
		close(backend.canceled)
	}
	return &catalogv1.ListSkillsResponse{Skills: []*catalogv1.PublicSkill{{Id: "old", Name: "Old owner result"}}}, nil
}

func TestNativeCancellationOldOwnerOriginAndCloseDrain(test *testing.T) {
	for _, scenario := range []string{"cancel", "origin", "logout", "close", "selection"} {
		test.Run(scenario, func(test *testing.T) {
			backend := &blockedBackend{started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
			model, pump := modelFixture(test, backend)
			model.SetOrigin("https://old.example")
			tester := ui.NewTester(model.View, 920, 760)
			click(test, tester, "搜索")
			<-backend.started
			switch scenario {
			case "cancel":
				click(test, tester, "取消任务")
			case "origin":
				model.SetOrigin("https://new.example")
			case "logout":
				model.Logout()
			case "close":
				model.Close()
			case "selection":
				model.Select(-1)
			}
			close(backend.release)
			pump()
			select {
			case <-backend.canceled:
			default:
				test.Fatal("worker context was not canceled")
			}
			if len(model.Skills) != 0 || model.Busy {
				test.Fatal("old result repopulated current UI")
			}
		})
	}
}

func TestUnsafeOriginsAndRedactedNetworkErrors(test *testing.T) {
	for _, origin := range []string{"http://example.org", "https://user:secret@example.org", "https://example.org/path", "https://example.org?q=secret", "https://example.org#secret", "http://127.0.0.1.example.org", "file:///tmp/server"} {
		if _, err := ValidateOrigin(origin); err == nil {
			test.Fatalf("unsafe origin accepted: %s", origin)
		}
	}
	for _, origin := range []string{"https://example.org/", "http://127.0.0.1:8080", "http://[::1]:8080", "http://localhost:8080"} {
		if _, err := ValidateOrigin(origin); err != nil {
			test.Fatal(err)
		}
	}
	if strings.Contains(networkError(errors.New("secret-password")).Error(), "secret-password") {
		test.Fatal("network error leaked remote content")
	}
}
