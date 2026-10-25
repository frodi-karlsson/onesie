package creds

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
		return Opened{Profiles: profiles}, nil
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
	if refusal := converted.Validate(); refusal != nil {
		return Opened{}, &InvalidProfilesError{
			Path: oldPath, Kind: credentialKind, Reason: "cannot become profiles: " + refusal.Error(),
		}
	}

	opened := Opened{Profiles: converted, Converted: slices.Sorted(maps.Keys(converted.Profiles))}

	warning, err := s.SaveProfiles(path, converted)
	if err != nil {
		opened.Warnings = append(opened.Warnings, fmt.Errorf(
			"onesie: could not write %s, so %s is converted for this run only: %w", path, oldPath, err))

		return opened, nil
	}

	if warning != nil {
		opened.Warnings = append(opened.Warnings, warning)
	}

	// Another process converting at the same moment may have removed it first.
	if err := s.remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		opened.Warnings = append(opened.Warnings, fmt.Errorf(
			"onesie: converted %s but could not remove it: %w", oldPath, err))
	}

	return opened, nil
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

	// A lone entry for another provider is not made the default, since a bare run used typesafe
	// before and would silently move to that provider and spend its key.
	if _, ok := profiles.Profiles[convertedDefault]; ok {
		profiles.Default = convertedDefault
	}

	return profiles
}

// Opened is what OpenProfiles found. Converted names the profiles made from credentials.json on this
// call, sorted, and Warnings are failures that did not stop it, each worth telling the user.
type Opened struct {
	Profiles  Profiles
	Converted []string
	Warnings  []error
}
