package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/patherr"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	imageRefPrefix = "image-"
	textKey        = "text"
)

func newImaging(
	specs, fields []string, lines bool, readFile func(string) ([]byte, error), body onesie.Assets,
) (*imaging, error) {
	m := &imaging{fields: fields, lines: lines, readFile: readFile, fixed: map[string]string{}, assets: onesie.Assets{}}
	maps.Copy(m.assets, body)

	for _, spec := range specs {
		name, path, ok := strings.Cut(spec, "=")
		if !ok || name == "" || path == "" {
			return nil, fmt.Errorf("--image takes NAME=PATH, got '%s'", spec)
		}

		if err := checkImageName("--image "+name, name); err != nil {
			return nil, err
		}

		if _, taken := m.fixed[name]; taken {
			return nil, fmt.Errorf("--image %s is given twice", name)
		}

		ref, img, err := m.load(path)
		if err != nil {
			return nil, fmt.Errorf("--image %s: %w", name, err)
		}

		m.fixed[name] = ref
		m.assets[ref] = img
	}

	for _, field := range fields {
		if err := checkImageName("--image-field "+field, field); err != nil {
			return nil, err
		}
	}

	return m, nil
}

type imaging struct {
	fixed    map[string]string
	fields   []string
	lines    bool
	readFile func(string) ([]byte, error)
	assets   onesie.Assets
}

func (m *imaging) wrapLine(state, wire any) (any, any) {
	if m == nil || !m.lines || len(m.fields) == 0 {
		return state, wire
	}

	wrapped := map[string]any{m.fields[0]: state}

	return wrapped, wrapped
}

func (m *imaging) attach(state any) (any, onesie.Assets, error) {
	if m == nil || (len(m.fixed) == 0 && len(m.fields) == 0) {
		return state, m.bodyAssets(), nil
	}

	flag := "--image-field"
	if len(m.fixed) > 0 {
		flag = "--image"
	}

	object, err := objectOf(state, flag)
	if err != nil {
		return nil, nil, err
	}

	assets := m.bodyAssets()

	for _, field := range m.fields {
		path, pathErr := fieldPath(object, field)
		if pathErr != nil {
			return nil, nil, fmt.Errorf("--image-field %s: %w", field, pathErr)
		}

		ref, img, loadErr := m.load(path)
		if loadErr != nil {
			return nil, nil, fmt.Errorf("--image-field %s: %w", field, loadErr)
		}

		object[field] = ref
		assets[ref] = img
	}

	for _, name := range slices.Sorted(maps.Keys(m.fixed)) {
		if _, taken := object[name]; taken {
			if name == textKey {
				return nil, nil, fmt.Errorf("--image %s: a text state goes under the key '%s', "+
					"so an image cannot be named %s", name, textKey, textKey)
			}

			return nil, nil, fmt.Errorf("--image %s: the state already has a key '%s'", name, name)
		}

		ref := m.fixed[name]
		object[name] = ref
		assets[ref] = m.assets[ref]
	}

	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding the state: %w", err)
	}

	return json.RawMessage(encoded), assets, nil
}

func (m *imaging) dropUnreferenced(questions onesie.Questions) (int, error) {
	if m == nil {
		return 0, nil
	}

	body := m.bodyAssets()

	unreferenced, err := onesie.UnreferencedAssets(onesie.Request{Questions: questions, Assets: body})
	if err != nil {
		return 0, err
	}

	for _, ref := range unreferenced {
		delete(m.assets, ref)
	}

	return len(unreferenced), nil
}

func (m *imaging) fingerprint() []string {
	if m == nil {
		return nil
	}

	var parts []string

	for _, field := range m.fields {
		parts = append(parts, "field "+field)
	}

	for _, name := range slices.Sorted(maps.Keys(m.fixed)) {
		parts = append(parts, "image "+name+"="+m.fixed[name])
	}

	for _, ref := range slices.Sorted(maps.Keys(m.bodyAssets())) {
		sum := sha256.Sum256(m.assets[ref].Data)
		parts = append(parts, "asset "+ref+"="+hex.EncodeToString(sum[:]))
	}

	return parts
}

func (m *imaging) bodyAssets() onesie.Assets {
	assets := onesie.Assets{}
	if m == nil {
		return assets
	}

	for ref, img := range m.assets {
		if !slices.Contains(slices.Collect(maps.Values(m.fixed)), ref) {
			assets[ref] = img
		}
	}

	return assets
}

func (m *imaging) load(path string) (string, onesie.Image, error) {
	data, err := m.readFile(path)
	if err != nil {
		return "", onesie.Image{}, fmt.Errorf("reading %s: %w", path, patherr.Inner(err))
	}

	img, err := onesie.NewImage(data)
	if err != nil {
		return "", onesie.Image{}, fmt.Errorf("%s: %w", path, unprefixed(err))
	}

	// Checked here rather than when the request is built, so a file the API would refuse costs no
	// request and names its own path.
	if err := onesie.ValidateImage(path, img); err != nil {
		return "", onesie.Image{}, unprefixed(err)
	}

	return imageRef(data), img, nil
}

func checkProviderNeeds(settings rootSettings, flags runConfig, built *plan.Plan, images *imaging) error {
	// A dry run sends nothing, and the body it prints is the provider neutral one.
	if flags.printRequest {
		return nil
	}

	chosen, err := runResolution(settings)
	if err != nil {
		return err
	}

	model, err := resolveModel(settings, built.Model)
	if err != nil {
		return err
	}

	return longContextCause(chosen.provider.Refuse(model, onesie.Needs{
		Images:      sendsAssets(images),
		Box:         asksBox(built),
		LongContext: built.LongContext,
	}), settings, flags)
}

func longContextCause(refusal error, settings rootSettings, flags runConfig) error {
	// The variable is easy to forget once it is exported, so a refusal it caused says so.
	var lacking *onesie.CapabilityError
	if !errors.As(refusal, &lacking) || !lacking.Missing.LongContext || flags.longContextSet ||
		!longContext(settings, flags) {
		return refusal
	}

	return &advisedError{
		cause: refusal,
		message: refusal.Error() + ". " + envLongContext +
			" turned it on, so unset it or pass --long-context=false",
	}
}

func sendsAssets(images *imaging) bool {
	return images != nil && (len(images.assets) > 0 || len(images.fields) > 0)
}

func asksBox(built *plan.Plan) bool {
	return slices.ContainsFunc(built.Questions, func(question plan.Question) bool {
		return question.Shape == plan.Box
	})
}

func imageRef(data []byte) string {
	sum := sha256.Sum256(data)

	return imageRefPrefix + hex.EncodeToString(sum[:16])
}

func checkImageName(flag, name string) error {
	if strings.ContainsAny(name, ".[]`") {
		return fmt.Errorf("%s: a name holds no '.', '[', ']' or backtick, "+
			"so a backtick path in a question can reach it", flag)
	}

	return nil
}

func objectOf(state any, flag string) (map[string]any, error) {
	switch typed := state.(type) {
	case nil:
		return map[string]any{}, nil
	case string:
		return map[string]any{textKey: typed}, nil
	case map[string]any:
		return maps.Clone(typed), nil
	case json.RawMessage:
		decoder := json.NewDecoder(bytes.NewReader(typed))
		decoder.UseNumber()

		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("reading the state: %w", err)
		}

		return objectOf(value, flag)
	case []any:
		return nil, fmt.Errorf("%s needs an object or a text state to sit beside, got an array", flag)
	default:
		return nil, fmt.Errorf("%s needs an object or a text state to sit beside, got %s", flag, jsonKind(state))
	}
}

func fieldPath(object map[string]any, field string) (string, error) {
	value, found := object[field]
	if !found {
		return "", fmt.Errorf("the record has no '%s'", field)
	}

	path, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("the value is %s, not a path", jsonKind(value))
	}

	return path, nil
}

func jsonKind(value any) string {
	switch value.(type) {
	case json.Number, float64:
		return "a number"
	case bool:
		return "a boolean"
	case nil:
		return "null"
	case []any:
		return "an array"
	default:
		return "an object"
	}
}

func unprefixed(err error) error {
	return errors.New(strings.TrimPrefix(err.Error(), "onesie: "))
}
