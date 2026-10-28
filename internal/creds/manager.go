package creds

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
)

// The kinds of Notice.
const (
	// SaveWarning is a failure after the profiles file was written that did not stop the write, in Err.
	SaveWarning NoticeKind = iota + 1
	// KeyInKeychain means the key went into the keychain item Account.
	KeyInKeychain
	// KeyInFile means the keychain refused the key with Err, so it went into the profiles file.
	KeyInFile
	// FirstDefault means the profile created became the default, as the first profile.
	FirstDefault
	// DefaultDeleted means the profile deleted was the default, so no profile is the default now.
	DefaultDeleted
	// ItemLeftBehind means the keychain item Account could not be removed, for Err.
	ItemLeftBehind
	// OldKeyUnread means the keychain item Account holds the new key, and its old key could not be read
	// to put back.
	OldKeyUnread
	// OldKeyLost means Err kept the old key from going back into the keychain item Account, which holds
	// the new key.
	OldKeyLost
)

// NewProfileManager builds a ProfileManager that writes profiles through store and keeps their keys
// in keychain.
func NewProfileManager(store Store, keychain *Keychain) *ProfileManager {
	return &ProfileManager{store: store, keychain: keychain}
}

// ProfileManager creates, updates and deletes profiles, keeping profiles.json and the keychain items
// in step. Each change returns notices for the user, also beside an error.
type ProfileManager struct {
	store    Store
	keychain *Keychain
}

// Create stores a new profile and its key, in the keychain unless ToFile says the file. The first
// profile becomes the default.
func (m *ProfileManager) Create(change Change) ([]Notice, error) {
	account := keychainAccount(change.Name, change.Path)

	if refused := refuseTakenItem(change.Profiles, change.Name, account, change.ToFile); refused != nil {
		return nil, refused
	}

	key, err := change.ReadKey()
	if err != nil {
		return nil, err
	}

	stored, notices, err := m.storeKey(change.Profile, key, account, change.ToFile)
	if err != nil {
		return nil, err
	}

	profiles := change.Profiles.With(change.Name, stored)

	if len(change.Profiles.Profiles) == 0 {
		profiles.Default = change.Name
		notices = append(notices, Notice{Kind: FirstDefault})
	}

	warnings, err := m.save(change.Path, profiles)
	if err != nil {
		if stored.Store == StoreKeychain {
			return m.undoneItem(account), err
		}

		return nil, err
	}

	return append(warnings, notices...), nil
}

// Update writes the changed profile. With ReadKey it replaces the key too, and puts the old key back
// when the file cannot be written.
func (m *ProfileManager) Update(change Change) ([]Notice, error) {
	if change.ReadKey == nil {
		return m.save(change.Path, change.Profiles.With(change.Name, change.Profile))
	}

	account := keychainAccount(change.Name, change.Path)

	if refused := refuseTakenItem(change.Profiles, change.Name, account, change.ToFile); refused != nil {
		return nil, refused
	}

	key, err := change.ReadKey()
	if err != nil {
		return nil, err
	}

	oldItem := keychainItem(change.Profiles.Profiles[change.Name])

	kept, err := m.keepKey(oldItem, account, change.ToFile)
	if err != nil {
		return nil, err
	}

	stored, notices, err := m.storeKey(change.Profile, key, account, change.ToFile)
	if err != nil {
		return nil, err
	}

	warnings, err := m.save(change.Path, change.Profiles.With(change.Name, stored))
	if err != nil {
		if stored.Store == StoreKeychain {
			return m.undo(kept, account), err
		}

		return nil, err
	}

	notices = append(warnings, notices...)

	// Only once saved does the file stop naming the replaced item, so it can go.
	if oldItem != "" && oldItem != stored.Account && !sharedItem(change.Profiles, change.Name, oldItem) {
		if deleteErr := m.keychain.Delete(oldItem); deleteErr != nil {
			notices = append(notices, Notice{Kind: ItemLeftBehind, Account: oldItem, Err: deleteErr})
		}
	}

	return notices, nil
}

// Delete removes the named profile, then its keychain item unless another profile names it. Deleting
// the last profile removes the file.
func (m *ProfileManager) Delete(change Change) ([]Notice, error) {
	path, profiles, name := change.Path, change.Profiles, change.Name
	rest := profiles.without(name)

	var notices []Notice

	// With the last profile goes any credentials.json beside it, so a later run does not convert
	// the deleted profiles back.
	if len(rest.Profiles) == 0 {
		warnings, err := m.store.ClearProfilesIn(filepath.Dir(path))
		if err != nil {
			return nil, err
		}

		notices = warningNotices(warnings)
	} else {
		warnings, err := m.save(path, rest)
		if err != nil {
			return nil, err
		}

		notices = warnings
	}

	if profiles.Default == name && len(rest.Profiles) > 0 {
		notices = append(notices, Notice{Kind: DefaultDeleted})
	}

	// The file names the item until the save, so the delete waits for it. A hand written file can name
	// one item from two profiles, and the other one still needs it.
	if account := keychainItem(profiles.Profiles[name]); account != "" && !sharedItem(profiles, name, account) {
		if deleteErr := m.keychain.Delete(account); deleteErr != nil {
			notices = append(notices, Notice{Kind: ItemLeftBehind, Account: account, Err: deleteErr})
		}
	}

	return notices, nil
}

// Change names one profile in the profiles file at Path, which holds Profiles now. ReadKey reads the
// new key, Update keeps the key when ReadKey is nil, and Delete needs no Profile, ToFile or ReadKey.
type Change struct {
	Path     string
	Profiles Profiles
	Name     string
	Profile  Profile
	ToFile   bool
	ReadKey  func() (string, error)
}

// Notice is one thing a profile change tells the user beside its outcome. Kind says what, and
// Account and Err fill it in.
type Notice struct {
	Kind    NoticeKind
	Account string
	Err     error
}

// NoticeKind names what a Notice reports.
type NoticeKind int

func refuseTakenItem(profiles Profiles, name, account string, toFile bool) error {
	owner := itemOwner(profiles, name, account)
	if toFile || owner == "" {
		return nil
	}

	return fmt.Errorf("profile %s already uses the keychain item %s. Delete or update it first", QuotedName(owner), account)
}

func sharedItem(profiles Profiles, except, account string) bool {
	return itemOwner(profiles, except, account) != ""
}

func itemOwner(profiles Profiles, except, account string) string {
	for _, name := range slices.Sorted(maps.Keys(profiles.Profiles)) {
		if name != except && keychainItem(profiles.Profiles[name]) == account {
			return name
		}
	}

	return ""
}

func keychainItem(profile Profile) string {
	if profile.Store != StoreKeychain {
		return ""
	}

	return profile.Account
}

func (m *ProfileManager) storeKey(
	profile Profile, key, account string, toFile bool,
) (stored Profile, notices []Notice, err error) {
	profile.APIKey, profile.Store, profile.Account, profile.KeyEnd = "", "", "", ""

	if toFile {
		profile.APIKey = key

		return profile, nil, nil
	}

	err = m.keychain.Set(account, key)
	if err == nil {
		profile.Store, profile.Account, profile.KeyEnd = StoreKeychain, account, KeyEnd(key)

		return profile, []Notice{{Kind: KeyInKeychain, Account: account}}, nil
	}

	// A keychain that timed out may still be waiting on a prompt that stores the key later, so a
	// fallback here could leave the key in both places.
	if errors.Is(err, ErrKeychainTimeout) {
		return Profile{}, nil, err
	}

	profile.APIKey = key

	return profile, []Notice{{Kind: KeyInFile, Err: err}}, nil
}

func (m *ProfileManager) save(path string, profiles Profiles) ([]Notice, error) {
	warnings, err := m.store.SaveProfilesIn(filepath.Dir(path), profiles)
	if err != nil {
		return nil, err
	}

	return warningNotices(warnings), nil
}

func warningNotices(warnings []error) []Notice {
	notices := make([]Notice, 0, len(warnings))
	for _, warning := range warnings {
		notices = append(notices, Notice{Kind: SaveWarning, Err: warning})
	}

	return notices
}

func (m *ProfileManager) keepKey(oldItem, account string, toFile bool) (keptKey, error) {
	if toFile || oldItem != account {
		return keptKey{}, nil
	}

	key, err := m.keychain.Get(account)

	// A keychain that did not answer a read would not answer a write either, and a write to it
	// could land later, after onesie has given up on it.
	if errors.Is(err, ErrKeychainTimeout) {
		return keptKey{}, err
	}

	return keptKey{
		same: true, key: key, found: err == nil, missing: errors.Is(err, ErrKeychainMissing),
	}, nil
}

func (m *ProfileManager) undo(kept keptKey, account string) []Notice {
	// A new item, or one that held no key, would keep a key no saved file chose.
	if !kept.same || kept.missing {
		return m.undoneItem(account)
	}

	if !kept.found {
		return []Notice{{Kind: OldKeyUnread, Account: account}}
	}

	if err := m.keychain.Set(account, kept.key); err != nil {
		return []Notice{{Kind: OldKeyLost, Account: account, Err: err}}
	}

	return nil
}

func (m *ProfileManager) undoneItem(account string) []Notice {
	if err := m.keychain.Delete(account); err != nil {
		return []Notice{{Kind: ItemLeftBehind, Account: account, Err: err}}
	}

	return nil
}

type keptKey struct {
	same    bool
	key     string
	found   bool
	missing bool
}

// With returns a copy of the profiles that holds profile under name.
func (p Profiles) With(name string, profile Profile) Profiles {
	changed := Profiles{Default: p.Default, Profiles: maps.Clone(p.Profiles)}
	if changed.Profiles == nil {
		changed.Profiles = map[string]Profile{}
	}

	changed.Profiles[name] = profile

	return changed
}

func (p Profiles) without(name string) Profiles {
	rest := Profiles{Default: p.Default, Profiles: maps.Clone(p.Profiles)}
	delete(rest.Profiles, name)

	if rest.Default == name {
		rest.Default = ""
	}

	return rest
}
