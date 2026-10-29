package desktop

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/ca-x/cognate/gen/skillhub/catalog/v1/catalogv1connect"
	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/importclient"
	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/packclient"
	"github.com/ca-x/cognate/internal/state"
	"github.com/ca-x/cognate/internal/subscription"
)

type Service struct {
	Home string
	HTTP *http.Client
}

func ValidateOrigin(raw string) (string, error) {
	if _, err := packclient.NewPublic(raw, nil); err != nil {
		return "", errors.New("服务地址无效：需要 HTTPS 或明确 loopback HTTP origin")
	}
	origin, err := url.Parse(raw)
	if err != nil || origin == nil || origin.Host == "" || origin.User != nil || origin.Opaque != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || origin.RawPath != "" || (origin.Path != "" && origin.Path != "/") {
		return "", errors.New("服务地址必须是 HTTPS origin；禁止路径、凭据、查询和片段")
	}
	host := strings.ToLower(origin.Hostname())
	address := net.ParseIP(host)
	loopback := host == "localhost" || address != nil && address.IsLoopback()
	if origin.Scheme != "https" && !(origin.Scheme == "http" && loopback) {
		return "", errors.New("仅允许 HTTPS 或明确的 loopback HTTP 地址")
	}
	if strings.ContainsAny(raw, "\r\n\t ") {
		return "", errors.New("服务地址包含空白")
	}
	origin.Path = ""
	origin.Host = strings.ToLower(origin.Host)
	return origin.String(), nil
}

type originTransport struct {
	base   http.RoundTripper
	origin string
}

func (transport originTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	expected, err := ValidateOrigin(transport.origin)
	if err != nil {
		return nil, err
	}
	if request.URL.Scheme+"://"+request.URL.Host != expected || request.URL.User != nil || request.URL.RawQuery != "" || request.URL.Fragment != "" {
		return nil, errors.New("拒绝跨 origin 请求")
	}
	return transport.base.RoundTrip(request)
}

func (service *Service) client(origin string) *http.Client {
	client := service.clientForPacks()
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = originTransport{base: base, origin: origin}
	return client
}

func (service *Service) clientForPacks() *http.Client {
	client := &http.Client{Timeout: 45 * time.Second}
	if service.HTTP != nil {
		*client = *service.HTTP
	}
	client.Jar = nil
	client.Timeout = 45 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

func networkError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return fmt.Errorf("服务请求失败 (%s)；请检查地址、权限或连接", connect.CodeOf(err))
}

func (service *Service) Login(ctx context.Context, origin, username, password string) (string, error) {
	client := authv1connect.NewAuthServiceClient(service.client(origin), origin, connect.WithReadMaxBytes(4096))
	response, err := client.Login(ctx, connect.NewRequest(&authv1.LoginRequest{Username: username, Password: password}))
	if err != nil {
		return "", networkError(err)
	}
	if _, err := importclient.ReadSession(strings.NewReader(response.Msg.SessionToken)); err != nil {
		return "", errors.New("服务未返回有效会话")
	}
	return response.Msg.SessionToken, nil
}

func (service *Service) Logout(ctx context.Context, origin, token string) error {
	request := connect.NewRequest(&authv1.LogoutRequest{})
	request.Header().Set("Authorization", "Bearer "+token)
	_, err := authv1connect.NewAuthServiceClient(service.client(origin), origin, connect.WithReadMaxBytes(4096)).Logout(ctx, request)
	return networkError(err)
}

func (service *Service) Search(ctx context.Context, origin, query, cursor string) (*catalogv1.ListSkillsResponse, error) {
	client := catalogv1connect.NewPublicCatalogServiceClient(service.client(origin), origin, connect.WithReadMaxBytes(512<<10))
	response, err := client.ListSkills(ctx, connect.NewRequest(&catalogv1.ListSkillsRequest{Query: query, Cursor: cursor, Limit: 50}))
	if err != nil {
		return nil, networkError(err)
	}
	if len(response.Msg.Skills) > 50 {
		return nil, errors.New("目录响应超出限制")
	}
	for _, entry := range response.Msg.Skills {
		if entry == nil || entry.Id == "" || entry.Name == "" {
			return nil, errors.New("目录响应无效")
		}
	}
	return response.Msg, nil
}

func (service *Service) Details(ctx context.Context, origin, id string) (*catalogv1.PublicSkill, error) {
	client, err := packclient.NewPublic(origin, service.client(origin))
	if err != nil {
		return nil, err
	}
	entry, err := client.GetSkill(ctx, id)
	return entry, networkError(err)
}

func (service *Service) Install(ctx context.Context, origin string, selected *catalogv1.PublicSkill) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.GOOS != "linux" {
		return errUnsupportedLocal
	}
	if selected == nil {
		return errors.New("请选择有效技能")
	}
	client, err := packclient.NewPublic(origin, service.client(origin))
	if err != nil {
		return err
	}
	key, err := clientkey.LoadExisting(ctx, service.Home)
	clear(key)
	if err != nil {
		return err
	}
	directory, err := prepareHome(service.Home)
	if err != nil {
		return err
	}
	snapshot, err := client.Download(ctx, selected)
	if err != nil {
		return networkError(err)
	}
	defer func() { resultErr = errors.Join(resultErr, snapshot.Close()) }()
	session, err := install.OpenSession(ctx, directory)
	if err != nil {
		return err
	}
	_, err = session.InstallLocalWithHash(ctx, service.Home, selected.Slug, snapshot.Path(), selected.ContentHash)
	return errors.Join(err, session.Close())
}

func (service *Service) openPackManager(ctx context.Context) (*subscription.Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" {
		return nil, errUnsupportedLocal
	}
	key, err := clientkey.LoadExisting(ctx, service.Home)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	directory, err := prepareHome(service.Home)
	if err != nil {
		return nil, err
	}
	return subscription.Open(ctx, service.Home, directory, key, service.clientForPacks())
}

func (service *Service) InitializeClientKey(ctx context.Context) error {
	return clientkey.Initialize(ctx, service.Home)
}

func (service *Service) Packs(ctx context.Context, action, origin, token, id string) (records []state.Subscription, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch action {
	case "subscribe":
		if _, err := packclient.New(origin, token, service.client(origin)); err != nil {
			return nil, networkError(err)
		}
	case "list", "sync", "remove":
	default:
		return nil, errors.New("未知 Pack 操作")
	}
	manager, err := service.openPackManager(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, manager.Close()) }()
	switch action {
	case "subscribe":
		_, err = manager.Subscribe(ctx, origin, token)
	case "sync":
		_, err = manager.Sync(ctx)
	case "remove":
		err = manager.Unsubscribe(ctx, id)
	case "list":
	default:
		return nil, errors.New("未知 Pack 操作")
	}
	if err != nil {
		return nil, err
	}
	return manager.List(ctx)
}

func (service *Service) Preview(ctx context.Context, origin, session, path string) (*importclient.Client, *importclient.Preview, error) {
	client, err := importclient.New(origin, session, service.client(origin))
	if err != nil {
		return nil, nil, err
	}
	preview, err := client.PreviewFile(ctx, path)
	return client, preview, err
}
