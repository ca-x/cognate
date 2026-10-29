package logging

import (
	"bytes"
	"context"
	"log/slog"
	"time"
)

type EventKind uint8

const (
	EventServerStarted EventKind = iota + 1
	EventServerStopped
	EventServerFailed
	EventRequestStarted
	EventRequestCompleted
	EventTransportError
)

type Route uint8

const (
	RouteHealth Route = iota + 1
	RouteAuth
	RouteCatalog
	RouteLibrary
	RoutePacks
	RouteDistribution
	RouteGitHub
	RouteImport
	RouteMCP
	RouteConfiguration
	RouteNativeConfiguration
	RouteManagement
	RouteWeb
	RouteOther
)

type Event struct {
	Kind     EventKind
	Route    Route
	Status   int
	Duration time.Duration
}

func (event Event) level() (slog.Level, error) {
	if event.Kind < EventServerStarted || event.Kind > EventTransportError {
		return 0, ErrInvalid
	}
	if event.Kind == EventRequestStarted || event.Kind == EventRequestCompleted {
		if event.Route < RouteHealth || event.Route > RouteOther || event.Duration < 0 {
			return 0, ErrInvalid
		}
		if event.Kind == EventRequestStarted {
			if event.Status != 0 || event.Duration != 0 {
				return 0, ErrInvalid
			}
			return slog.LevelDebug, nil
		}
		if event.Status < 100 || event.Status > 599 {
			return 0, ErrInvalid
		}
		if event.Status >= 500 {
			return slog.LevelError, nil
		}
		if event.Status >= 400 {
			return slog.LevelWarn, nil
		}
		return slog.LevelInfo, nil
	}
	if event.Route != 0 || event.Status != 0 || event.Duration != 0 {
		return 0, ErrInvalid
	}
	if event.Kind == EventServerFailed || event.Kind == EventTransportError {
		return slog.LevelError, nil
	}
	return slog.LevelInfo, nil
}

func encodeEvent(event Event, level slog.Level, now time.Time) ([]byte, error) {
	names := [...]string{"", "server_started", "server_stopped", "server_failed", "request_started", "request_completed", "transport_error"}
	routes := [...]string{"", "health", "auth", "catalog", "library", "packs", "distribution", "github", "import", "mcp", "configuration", "native_configuration", "management", "web", "other"}
	var destination bytes.Buffer
	handler := slog.NewJSONHandler(&destination, &slog.HandlerOptions{Level: slog.LevelDebug})
	record := slog.NewRecord(now.UTC(), level, names[event.Kind], 0)
	if event.Route != 0 {
		record.AddAttrs(slog.String("route", routes[event.Route]))
	}
	if event.Kind == EventRequestCompleted {
		record.AddAttrs(slog.Int("status", event.Status), slog.Int64("duration_ns", int64(event.Duration)))
	}
	if err := handler.Handle(context.Background(), record); err != nil || destination.Len() > 1024 {
		return nil, ErrUnavailable
	}
	return destination.Bytes(), nil
}
