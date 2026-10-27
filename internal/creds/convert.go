package creds

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/frodi-karlsson/onesie-go"
)

const convertedDefault = "typesafe"

// OpenProfiles reads the profiles file in dir. When only credentials.json is there, it converts it
// once, writes profiles.json and removes the old file.
func (s Store) OpenProfiles(dir string) (Opened, error) {
	path := filepath.Join(dir, profilesName)

	profiles, found, err := s.LoadProfiles(path)
	if err != nil {
		return Opened{}, err
	}

	if found {
		return Opened{Profiles: profiles, Found: true}, nil
	}

	oldPath := filepath.Join(dir, fileName)

	file, found, err := s.Load(oldPath)
	if err != nil {
		return Opened{}, err
	}

	if !found {
		return Opened{Profiles: Profiles{Profiles: map[string]Profile{}}}, nil
	}

	converted := Convert(file)
	if refusal := converted.validateEntries(); refusal != nil {
		return Opened{}, invalidFile(oldPath, credentialKind, "cannot become profiles", refusal.Error())
	}

	opened := Opened{Profiles: converted, Converted: slices.Sorted(maps.Keys(converted.Profiles)), Found: true}

	warnings, err := s.SaveProfilesIn(dir, converted)
	if err != nil {
		opened.InMemory = true
		opened.Warnings = append(opened.Warnings, fmt.Errorf(
			"could not write %s, so %s is converted for this run only: %w",
			path, oldPath, err))

		return opened, nil
	}

	opened.Warnings = append(opened.Warnings, warnings...)

	return opened, nil
}

// SaveProfilesIn writes profiles.json in dir and removes the credentials.json it replaces. Warnings
// are failures after the write that did not stop it, each worth telling the user.
func (s Store) SaveProfilesIn(dir string, profiles Profiles) (warnings []error, err error) {
	warning, err := s.SaveProfiles(filepath.Join(dir, profilesName), profiles)
	if err != nil {
		return nil, err
	}

	if warning != nil {
		warnings = append(warnings, warning)
	}

	oldPath := filepath.Join(dir, fileName)

	if removal := s.removeCredentials(oldPath, "converted "+oldPath+" but could not remove it"); removal != nil {
		warnings = append(warnings, removal)
	}

	return warnings, nil
}

// ClearProfilesIn removes profiles.json in dir and any credentials.json left beside it, so deleting
// the last profile is not undone by a later conversion. Warnings are removals that failed after it.
func (s Store) ClearProfilesIn(dir string) (warnings []error, err error) {
	if clearErr := s.clear(filepath.Join(dir, profilesName), profilesKind); clearErr != nil {
		return nil, clearErr
	}

	oldPath := filepath.Join(dir, fileName)

	if removal := s.removeCredentials(oldPath, "could not remove "+oldPath); removal != nil {
		warnings = append(warnings, removal)
	}

	return warnings, nil
}

func (s Store) removeCredentials(oldPath, failure string) error {
	info, statErr := s.lstat(oldPath)
	if errors.Is(statErr, os.ErrNotExist) {
		return nil
	}

	isLink := statErr == nil && info.Mode()&os.ModeSymlink != 0

	target := "the file it points to"
	if isLink {
		if resolved, linkErr := s.readlink(oldPath); linkErr == nil {
			target = "its target " + resolved
		}
	}

	// Another process converting at the same moment may have removed it first.
	if err := s.remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %w", failure, err)
	}

	if !isLink {
		return nil
	}

	return fmt.Errorf("removed the link %s, but %s still holds the key", oldPath, target)
}

// Convert turns a credential file into profiles, one per provider and named after it. Keychain
// items keep their names, and only a typesafe entry becomes the default.
func Convert(file File) Profiles {
	profiles := Profiles{Profiles: make(map[string]Profile, len(file.Providers))}

	for name, entry := range file.Providers {
		profile := Profile{Provider: name, APIKey: entry.APIKey, BaseURL: entry.BaseURL}

		if entry.Store == StoreKeychain {
			profile.Store = StoreKeychain
			profile.Account = entry.Account

			if profile.Account == "" {
				profile.Account = name
			}
		}

		profiles.Profiles[name] = profile
	}

	// A lone entry for another provider is not made the default, so a bare run asks for a choice
	// instead of silently moving to that provider and spending its key.
	if _, ok := profiles.Profiles[convertedDefault]; ok {
		profiles.Default = convertedDefault
	}

	return profiles
}

func (p Profiles) validateEntries() error {
	// An entry is named after its provider, so an unknown provider is an entry name, not a profile.
	for _, name := range slices.Sorted(maps.Keys(p.Profiles)) {
		if provider, err := onesie.ProviderNamed(name); err != nil || provider.Name != name {
			return fmt.Errorf(
				"its entry '%s' is not a provider. Use typesafe, openrouter, berget, levanto or cloudflare", name)
		}
	}

	return p.validateAs(func(name string) string { return fmt.Sprintf("its entry '%s'", name) })
}

// Opened is what OpenProfiles found. Found is false with neither file, Converted names the profiles
// converted on this call, InMemory means profiles.json was not written, and Warnings did not stop it.
type Opened struct {
	Profiles  Profiles
	Found     bool
	Converted []string
	InMemory  bool
	Warnings  []error
}
