package subscription_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/cli"
	"github.com/ca-x/cognate/internal/install"
	serverstate "github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
)

type watchOutput struct {
	bytes.Buffer
	events chan string
}

func (output *watchOutput) Write(data []byte) (int, error) {
	count, err := output.Buffer.Write(data)
	message := string(data)
	if strings.HasPrefix(message, "同步完成：") || strings.HasPrefix(message, "同步未完成") {
		select {
		case output.events <- message:
		default:
		}
	}
	return count, err
}

func (output *watchOutput) WriteContext(ctx context.Context, data []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return output.Write(data)
}

func TestCLIWatchUpdatesOverlappingPacksAndReleasesIdleLock(test *testing.T) {
	setup := newFixture(test)
	first, _ := setup.newSkill(test, "First")
	second, expected := setup.newSkill(test, "Second")
	firstPack, firstToken := setup.newPack(test, "Watch first", first.ID)
	secondPack, secondToken := setup.newPack(test, "Watch second", first.ID)
	test.Setenv("HOME", setup.home)
	for _, token := range []string{firstToken, secondToken} {
		command := cli.New()
		command.SetArgs([]string{"pack", "subscribe", "--server", setup.server.URL, "--token-stdin"})
		command.SetIn(strings.NewReader(token + "\n"))
		command.SetOut(new(bytes.Buffer))
		if err := command.ExecuteContext(test.Context()); err != nil {
			test.Fatal("could not prepare isolated CLI subscriptions")
		}
	}
	output := &watchOutput{events: make(chan string, 16)}
	command := cli.New()
	command.SetArgs([]string{"pack", "watch", "--interval=1s", "--attempt-timeout=5s"})
	command.SetOut(output)
	command.SetErr(output)
	ctx, cancel := context.WithCancel(test.Context())
	completed := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		completed <- command.ExecuteContext(ctx)
		close(finished)
	}()
	test.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			test.Error("watch did not shut down")
		}
	})
	waitReport := func() string {
		test.Helper()
		select {
		case message := <-output.events:
			return message
		case <-finished:
			test.Fatal("watch stopped before expected report")
		case <-time.After(8 * time.Second):
			test.Fatal("watch did not produce a bounded cycle")
		}
		return ""
	}
	if !strings.Contains(waitReport(), "复用 1") {
		test.Fatal("watch did not immediately reuse the existing installation")
	}
	lockCtx, stopLock := context.WithTimeout(test.Context(), 500*time.Millisecond)
	session, err := install.OpenSession(lockCtx, filepath.Join(setup.home, ".local", "share", "skillhub"))
	stopLock()
	if err != nil {
		test.Fatal("watch retained the installation lock while idle")
	}
	if err := session.Close(); err != nil {
		test.Fatal("could not release independent idle installation session")
	}
	for _, pack := range []serverstate.Pack{firstPack, secondPack} {
		if _, err := setup.store.UpdatePack(test.Context(), "alice", pack.ID, pack.Revision, serverstate.PackDraft{Name: pack.Name, Items: []serverstate.PackSelection{{SkillID: second.ID, Slug: "demo"}}}); err != nil {
			test.Fatal("could not update synthetic upstream packs")
		}
	}
	if !strings.Contains(waitReport(), "更新 1") {
		test.Fatal("automatic cycle did not update overlapping packs once")
	}
	canonical := filepath.Join(setup.home, ".agents", "skills", "demo")
	if actual, err := skill.ContentHash(afero.NewOsFs(), canonical); err != nil || actual != expected || setup.source.calls.Load() != 2 {
		test.Fatal("automatic update changed the wrong content or downloaded duplicate archives")
	}
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), []byte("local edit must survive"), 0o600); err != nil {
		test.Fatal(err)
	}
	if !strings.HasPrefix(waitReport(), "同步未完成") {
		test.Fatal("local edit was incorrectly reported as synchronized")
	}
	cancel()
	if err := <-completed; err != nil {
		test.Fatal("foreground cancellation returned an error")
	}
	<-finished
	content, err := os.ReadFile(filepath.Join(canonical, "SKILL.md"))
	if err != nil || string(content) != "local edit must survive" || setup.source.calls.Load() != 2 {
		test.Fatal("watch overwrote local edits or unnecessarily downloaded data")
	}
	if strings.Contains(output.String(), firstToken) || strings.Contains(output.String(), secondToken) {
		test.Fatal("watch output exposed a synthetic sharing credential")
	}
}
