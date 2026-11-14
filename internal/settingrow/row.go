// Package settingrow holds every run setting as one row: its flag, its environment variables, its
// question file key, its profile members, its kind and its help.
package settingrow

import (
	"maps"
	"slices"
	"strings"
)

// FlagOn returns the row's flag as command registers it, with the command's overrides applied, and
// whether command registers it at all.
func (r Row) FlagOn(command Command) (BoundFlag, bool) {
	if !slices.Contains(r.Flag.Commands, command) {
		return BoundFlag{}, false
	}

	bound := BoundFlag{
		Long: r.Flag.Long, Short: r.Flag.Short, Help: r.Help, Kind: r.Kind, Default: r.Default,
		HelpTopic: r.HelpTopic, HelpCommon: maps.Clone(r.HelpCommon), HelpValue: r.HelpValue, Needs: r.Needs,
	}
	override := r.Flag.PerCommand[command]

	if override.Help != "" {
		bound.Help = override.Help
	}

	if override.Kind != nil {
		bound.Kind = override.Kind
	}

	if override.Default != nil {
		bound.Default = override.Default
	}

	if override.Needs != nil {
		bound.Needs = override.Needs
	}

	if override.NoShort {
		bound.Short = ""
	}

	bound.Member = override.Member
	bound.Help = joinPhrases(bound.Help, r.phrases(command, bound.Needs))

	return bound, true
}

func (r Row) phrases(command Command, needs []Need) []string {
	var phrases []string

	if r.Flag.Repeat {
		phrases = append(phrases, "Repeatable.")
	}

	// Calibrate always reads a stream, so only the root run has a mode the flag is refused in.
	if command == CommandRoot && r.Modes.Has(ModeStream) && !r.Modes.Has(ModeAsk) {
		phrases = append(phrases, "Streaming only.")
	}

	if len(needs) > 0 {
		spelled := make([]string, 0, len(needs))
		for _, need := range needs {
			spelled = append(spelled, spellingOn(need.Name, command))
		}

		phrases = append(phrases, "Needs "+andList(spelled)+".")
	}

	return phrases
}

func joinPhrases(help string, phrases []string) string {
	if len(phrases) == 0 {
		return help
	}

	return strings.TrimSpace(help + " " + strings.Join(phrases, " "))
}

func spellingOn(name Name, command Command) string {
	for _, row := range Table() {
		if row.Name != name {
			continue
		}

		if row.Flag.Short != "" && !row.Flag.PerCommand[command].NoShort {
			return "-" + row.Flag.Short
		}

		return "--" + row.Flag.Long
	}

	return string(name)
}

func andList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}

	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// Row is one setting and every way to give it. InBody marks a FileKey that a request body holds,
// not a question file. Needs lists the settings its flag is refused without.
type Row struct {
	Name       Name
	Scope      Scope
	Flag       FlagSpec
	Env        []EnvSpec
	FileKey    string
	FileHelp   string
	InBody     bool
	Profile    []ProfileMember
	Kind       Kind
	Default    any
	Shapes     []Shape
	Modes      ModeSet
	Needs      []Need
	Help       string
	HelpTopic  string
	HelpCommon map[Command]int
	HelpValue  string
	Check      func(any) error
}

// BoundFlag is a row's flag on one command. Member is the profile member it writes, on a command
// that writes a profile.
type BoundFlag struct {
	Long, Short string
	Help        string
	HelpTopic   string
	HelpCommon  map[Command]int
	HelpValue   string
	Kind        Kind
	Default     any
	Member      ProfileMember
	Needs       []Need
}

// Need is a setting a flag is refused without, and the reason that ends the refusal, as in
// "--resume needs --out, the file it picks up from". HelpOnly marks a need another rule refuses.
type Need struct {
	Name     Name
	Reason   string
	HelpOnly bool
}

// Name is a setting's canonical name, as notices and rules spell it.
type Name string

// Scope says whether a setting holds once per run or once per question.
type Scope int

const (
	// ScopeRun is a setting that holds once for the whole run.
	ScopeRun Scope = iota
	// ScopeQuestion is a setting each question holds a value of.
	ScopeQuestion
)

// FlagSpec is how a row is given on the command line. A zero Long means no flag sets the row.
type FlagSpec struct {
	Long, Short string
	NoOptDefVal string
	Group       bool
	Repeat      bool
	AsksNothing bool
	Commands    []Command
	PerCommand  map[Command]CommandFlag
}

// CommandFlag overrides a row's flag on one command, where the flag means something narrower. Member
// names the profile member the flag writes on a command that writes a profile, and Needs replaces
// the row's needs.
type CommandFlag struct {
	Help    string
	Kind    Kind
	Default any
	NoShort bool
	Member  ProfileMember
	Needs   []Need
}

// Command names a command whose flags come from the rows.
type Command int

const (
	// CommandRoot is onesie itself, the run.
	CommandRoot Command = iota + 1
	// CommandCalibrate is onesie calibrate.
	CommandCalibrate
	// CommandProfileCreate is onesie profile create.
	CommandProfileCreate
	// CommandProfileUpdate is onesie profile update.
	CommandProfileUpdate
)

// String names the command as a user types it.
func (c Command) String() string {
	switch c {
	case CommandRoot:
		return "onesie"
	case CommandCalibrate:
		return "calibrate"
	case CommandProfileCreate:
		return "profile create"
	case CommandProfileUpdate:
		return "profile update"
	default:
		return "unknown"
	}
}

// EnvSpec is one environment variable that gives a row. Provider limits it to one provider, and
// Help and Order are its cell and its place in the Environment table, which groups by topic.
type EnvSpec struct {
	Name     string
	Provider string
	Help     string
	Order    int
	Half     EndpointHalf
}

// ProfileMember is one profiles.json member that gives a row.
type ProfileMember struct {
	Key  string
	Half EndpointHalf
}

// EndpointHalf says which half of the endpoint a source gives, since an account id and a base URL
// cannot be told apart by their text.
type EndpointHalf int

const (
	// HalfNone is a source of any row other than the endpoint.
	HalfNone EndpointHalf = iota
	// HalfBaseURL is a source that gives the API root.
	HalfBaseURL
	// HalfAccount is a source that gives a Cloudflare account id.
	HalfAccount
)

// Modes returns the set that holds each of modes.
func Modes(modes ...Mode) ModeSet {
	var set ModeSet
	for _, mode := range modes {
		set |= 1 << mode
	}

	return set
}

// ModeSet is a set of modes, one bit per Mode.
type ModeSet uint

// Has reports whether the set holds mode.
func (s ModeSet) Has(mode Mode) bool {
	return s&(1<<mode) != 0
}

// Mode is one way a command runs, which decides the settings that have an effect.
type Mode int

const (
	// ModeAsk asks the questions about one record.
	ModeAsk Mode = iota
	// ModeStream asks the questions about each record of a stream.
	ModeStream
	// ModeRequest sends each -i request body as written.
	ModeRequest
	// ModePrintRequest writes the request bodies and sends none.
	ModePrintRequest
	// ModePrintQuestions writes the questions as a question file.
	ModePrintQuestions
	// ModePrintSchema writes the question file schema.
	ModePrintSchema
	// ModeListModels writes the models a provider serves.
	ModeListModels
	// ModeCalibrate scores the answers against labelled records.
	ModeCalibrate
	// ModeProfile writes a profile.
	ModeProfile
)
