package creds

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	if refusal := converted.Validate(); refusal != nil {
		return Opened{}, &InvalidProfilesError{
			Path: oldPath, Kind: credentialKind, Reason: "cannot become profiles: " + refusal.Error(),
		}
	}

	opened := Opened{Profiles: converted, Converted: slices.Sorted(maps.Keys(converted.Profiles)), Found: true}

	warnings, err := s.SaveProfilesIn(dir, converted)
	if err != nil {
		opened.Warnings = append(opened.Warnings, fmt.Errorf(
			"onesie: could not write %s, so %s is converted for this run only: %w",
			path, oldPath, withoutPrefix(err)))

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

	if removal := s.removeCredentials(filepath.Join(dir, fileName)); removal != nil {
		warnings = append(warnings, removal)
	}

	return warnings, nil
}

func (s Store) removeCredentials(oldPath string) error {
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
		return fmt.Errorf("onesie: converted %s but could not remove it: %w", oldPath, err)
	}

	if !isLink {
		return nil
	}

	return fmt.Errorf("onesie: removed the link %s, but %s still holds the key", oldPath, target)
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

// Opened is what OpenProfiles found. Found is false when neither file was there, Converted names the
// profiles made from credentials.json on this call, sorted, and Warnings are failures that did not stop it.
type Opened struct {
	Profiles  Profiles
	Found     bool
	Converted []string
	Warnings  []error
}

func withoutPrefix(err error) error {
	return unprefixed{err}
}

type unprefixed struct {
	error
}

func (u unprefixed) Error() string {
	return strings.TrimPrefix(u.error.Error(), "onesie: ")
}

func (u unprefixed) Unwrap() error {
	return u.error
}
