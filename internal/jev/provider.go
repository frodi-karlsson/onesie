package jev

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ProviderNamed returns the provider a --provider value or ONESIE_PROVIDER names.
func ProviderNamed(name string) (Provider, error) {
	switch strings.TrimSpace(name) {
	case "", "typesafe":
		return TypeSafe(), nil
	case "openrouter":
		return OpenRouter(), nil
	default:
		return Provider{}, &ValidationError{
			Message: "unknown provider " + name + ". Use typesafe or openrouter",
		}
	}
}

// TypeSafe is the TypeSafe System One API, the default provider.
func TypeSafe() Provider {
	return Provider{
		Name:            "typesafe",
		BaseURL:         DefaultBaseURL,
		EnvAPIKey:       EnvAPIKey,
		EnvBaseURL:      EnvBaseURL,
		EnvDefaultModel: EnvDefaultModel,
		requestIDHeader: "X-TypeSafe-Request-Id",
		modelsPath:      "/v1/models",
		decodeModels:    decodeTypeSafeModels,
	}
}

// OpenRouter serves the same model and wire protocol through OpenRouter.
func OpenRouter() Provider {
	return Provider{
		Name:            "openrouter",
		BaseURL:         "https://openrouter.ai/api",
		EnvAPIKey:       "OPENROUTER_API_KEY",
		requestIDHeader: "X-Generation-Id",
		modelsPath:      "/v1/models?output_modalities=decisions",
		decodeModels:    decodeOpenRouterModels,
	}
}

// Provider is a host that serves the System One API. Build one with TypeSafe or OpenRouter. An
// empty env var name means the provider reads nothing from the environment for that setting.
type Provider struct {
	Name            string
	BaseURL         string
	EnvAPIKey       string
	EnvBaseURL      string
	EnvDefaultModel string

	requestIDHeader string
	modelsPath      string
	decodeModels    func([]byte) ([]ModelCard, error)
}

// ResolveModel reports the model a request will carry under this provider. It exists so a caller
// that prints a request without building a client fills the model the way a client would.
func (p Provider) ResolveModel(model string, lookupEnv func(string) (string, bool)) string {
	return orDefault(orEnv(model, lookupEnv, p.EnvDefaultModel), DefaultModel)
}

func decodeTypeSafeModels(body []byte) ([]ModelCard, error) {
	var wire struct {
		Models []ModelCard `json:"models"`
	}

	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}

	if wire.Models == nil {
		return nil, errors.New("expected a models list")
	}

	return wire.Models, nil
}

func decodeOpenRouterModels(body []byte) ([]ModelCard, error) {
	var wire struct {
		Data []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
			Created     int64  `json:"created"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}

	if wire.Data == nil {
		return nil, errors.New("expected a models list")
	}

	cards := make([]ModelCard, 0, len(wire.Data))
	for _, model := range wire.Data {
		cards = append(cards, ModelCard{
			Name:        model.ID,
			Description: model.Description,
			ReleaseDate: time.Unix(model.Created, 0).UTC().Format(time.DateOnly),
		})
	}

	return cards, nil
}
