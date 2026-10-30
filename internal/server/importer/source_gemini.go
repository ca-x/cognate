package importer

import (
	"context"
	"encoding/json/jsontext"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
)

func parseGeminiSelection(ctx context.Context, name string, raw []byte) (configuration.Draft, []SelectionSlot, error) {
	if err := ctx.Err(); err != nil {
		return configuration.Draft{}, nil, err
	}
	root, err := sourceObject(raw)
	if err != nil {
		return configuration.Draft{}, nil, err
	}
	defer clearSourceObject(root)
	if !sourceFields(root, "env", "config") {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	environment := map[string]jsontext.Value{}
	if value, present := root["env"]; present {
		environment, err = sourceObject(value)
		if err != nil {
			return configuration.Draft{}, nil, err
		}
	}
	defer clearSourceObject(environment)
	if !sourceFields(environment, "GOOGLE_GEMINI_BASE_URL", "GEMINI_MODEL", "GEMINI_API_KEY", "GOOGLE_API_KEY") {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	slots, err := selectionSlots(environment, []struct{ field, slot string }{{"GEMINI_API_KEY", "gemini.api-key"}, {"GOOGLE_API_KEY", "gemini.legacy-api-key"}})
	if err != nil {
		return configuration.Draft{}, nil, err
	}
	provider := configuration.GeminiProvider{}
	for _, field := range []struct {
		key    string
		target **string
	}{{"GOOGLE_GEMINI_BASE_URL", &provider.Endpoint}, {"GEMINI_MODEL", &provider.EnvModel}} {
		if rawValue, present := environment[field.key]; present {
			value, err := sourceString(rawValue)
			if err != nil {
				return configuration.Draft{}, nil, err
			}
			*field.target = new(value)
		}
	}
	config := map[string]jsontext.Value{}
	if value, present := root["config"]; present {
		config, err = sourceObject(value)
		if err != nil {
			return configuration.Draft{}, nil, err
		}
	}
	defer clearSourceObject(config)
	if !sourceFields(config, "model", "security") {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	if value, present := config["model"]; present {
		model, err := sourceObject(value)
		if err != nil {
			return configuration.Draft{}, nil, err
		}
		defer clearSourceObject(model)
		if !sourceFields(model, "name") {
			return configuration.Draft{}, nil, ErrSelectionUnsupported
		}
		if rawName, present := model["name"]; present {
			value, err := sourceString(rawName)
			if err != nil {
				return configuration.Draft{}, nil, err
			}
			provider.SettingsModel = new(value)
		}
	}
	if value, present := config["security"]; present {
		security, err := sourceObject(value)
		if err != nil {
			return configuration.Draft{}, nil, err
		}
		defer clearSourceObject(security)
		if !sourceFields(security, "auth") {
			return configuration.Draft{}, nil, ErrSelectionUnsupported
		}
		if value, present := security["auth"]; present {
			authentication, err := sourceObject(value)
			if err != nil {
				return configuration.Draft{}, nil, err
			}
			defer clearSourceObject(authentication)
			if !sourceFields(authentication, "selectedType") {
				return configuration.Draft{}, nil, ErrSelectionUnsupported
			}
			if value, present := authentication["selectedType"]; present {
				selectedType, err := sourceString(value)
				if err != nil {
					return configuration.Draft{}, nil, err
				}
				switch selectedType {
				case "oauth-personal":
					provider.AuthMode = agentconfig.GeminiNativeLogin
				case "gemini-api-key":
					provider.AuthMode = agentconfig.GeminiAPIKeyAuth
				default:
					return configuration.Draft{}, nil, ErrSelectionUnsupported
				}
			}
		}
	}
	if provider.Endpoint != nil && (!selectionEndpoint(*provider.Endpoint, true) || provider.AuthMode == agentconfig.GeminiNativeLogin) {
		return configuration.Draft{}, nil, ErrSelectionUnsupported
	}
	for _, model := range []*string{provider.EnvModel, provider.SettingsModel} {
		if model != nil && !selectionModel(*model, 256) {
			return configuration.Draft{}, nil, ErrSelectionUnsupported
		}
	}
	return configuration.Draft{Name: name, GeminiProvider: &provider}, slots, ctx.Err()
}
