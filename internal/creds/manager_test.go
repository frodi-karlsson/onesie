package creds_test

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/creds"
)

var errDiskFull = errors.New("the disk is full")

func TestProfileManagerCreate(t *testing.T) {
	t.Parallel()

	locked := errors.New("the keychain is locked")
	key := "SECRET-0123456789"
	typesafe := creds.Profile{Provider: "typesafe"}

	tests := []struct {
		name        string
		profiles    creds.Profiles
		toFile      bool
		backend     *itemBackend
		failWrite   bool
		wantErr     error
		wantNotices []creds.Notice
		wantFile    *creds.Profiles
		wantItems   map[string]string
		wantNoRead  bool
	}{
		{
			name:    "should store the key in the keychain and make the first profile the default",
			backend: newItemBackend(nil),
			wantNotices: []creds.Notice{
				{Kind: creds.KeyInKeychain, Account: "work@{hash}"},
				{Kind: creds.FirstDefault},
			},
			wantFile: &creds.Profiles{Default: "work", Profiles: map[string]creds.Profile{"work": {
				Provider: "typesafe", Store: creds.StoreKeychain, Account: "work@{hash}", KeyEnd: "6789",
			}}},
			wantItems: map[string]string{"work@{hash}": key},
		},
		{
			name:        "should store the key in the file under ToFile",
			profiles:    homeOnly(),
			toFile:      true,
			backend:     newItemBackend(nil),
			wantNotices: []creds.Notice{},
			wantFile: &creds.Profiles{Default: "home", Profiles: map[string]creds.Profile{
				"home": {Provider: "typesafe", APIKey: "SECRET-HOME"},
				"work": {Provider: "typesafe", APIKey: key},
			}},
			wantItems: map[string]string{},
		},
		{
			name:     "should put the key in the file when the keychain refuses it",
			profiles: homeOnly(),
			backend:  &itemBackend{items: map[string]string{}, setErrs: []error{locked}},
			wantNotices: []creds.Notice{{
				Kind: creds.KeyInFile, Err: &creds.KeychainError{Op: "store the key in", Err: locked},
			}},
			wantFile: &creds.Profiles{Default: "home", Profiles: map[string]creds.Profile{
				"home": {Provider: "typesafe", APIKey: "SECRET-HOME"},
				"work": {Provider: "typesafe", APIKey: key},
			}},
			wantItems: map[string]string{},
		},
		{
			name: "should refuse an item another profile uses before reading the key",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{
				"home": {Provider: "typesafe", Store: creds.StoreKeychain, Account: "work@{hash}"},
			}},
			backend:    newItemBackend(nil),
			wantErr:    errors.New("profile 'home' already uses the keychain item work@{hash}. Delete or update it first"),
			wantNoRead: true,
			wantItems:  map[string]string{},
		},
		{
			name:      "should remove the new item when the file cannot be written",
			backend:   newItemBackend(nil),
			failWrite: true,
			wantErr:   errDiskFull,
			wantItems: map[string]string{},
		},
		{
			name:      "should name the item left behind when its removal fails too",
			backend:   &itemBackend{items: map[string]string{}, deleteErr: locked},
			failWrite: true,
			wantErr:   errDiskFull,
			wantNotices: []creds.Notice{{
				Kind: creds.ItemLeftBehind, Account: "work@{hash}",
				Err: &creds.KeychainError{Op: "remove the key from", Err: locked},
			}},
			wantItems: map[string]string{"work@{hash}": key},
		},
		{
			name:      "should fail without the file when the keychain times out",
			backend:   &itemBackend{items: map[string]string{}, setErrs: []error{creds.ErrKeychainTimeout}},
			wantErr:   creds.ErrKeychainTimeout,
			wantItems: map[string]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path, fill := profilesPath(t)

			read := false
			notices, err := newManager(tc.backend, tc.failWrite).Create(creds.Change{
				Path: path, Profiles: filledProfiles(tc.profiles, fill), Name: "work", Profile: typesafe,
				ToFile: tc.toFile, ReadKey: func() (string, error) {
					read = true

					return key, nil
				},
			})

			checkChange(t, path, fill, notices, err, changeWant{
				err: tc.wantErr, notices: tc.wantNotices, file: tc.wantFile, items: tc.wantItems, backend: tc.backend,
			})

			if read == tc.wantNoRead {
				t.Errorf("read the key = %v, want %v", read, !tc.wantNoRead)
			}
		})
	}
}

func TestProfileManagerUpdate(t *testing.T) {
	t.Parallel()

	locked := errors.New("the keychain is locked")
	newKey := "SECRET-ABCDEFGHI"
	inKeychain := func(account string) creds.Profiles {
		return creds.Profiles{Default: "work", Profiles: map[string]creds.Profile{
			"work": {Provider: "openrouter", Store: creds.StoreKeychain, Account: account, KeyEnd: "5678"},
		}}
	}
	replaced := &creds.Profiles{Default: "work", Profiles: map[string]creds.Profile{
		"work": {Provider: "openrouter", Store: creds.StoreKeychain, Account: "work@{hash}", KeyEnd: "FGHI"},
	}}

	tests := []struct {
		name        string
		profiles    creds.Profiles
		model       string
		withKey     bool
		backend     *itemBackend
		failWrite   bool
		wantErr     error
		wantNotices []creds.Notice
		wantFile    *creds.Profiles
		wantItems   map[string]string
	}{
		{
			name:        "should save the members without touching the keychain when there is no new key",
			profiles:    inKeychain("work@{hash}"),
			model:       "typesafe/jev",
			backend:     newItemBackend(map[string]string{"work@{hash}": "SECRET-OLD"}),
			wantNotices: []creds.Notice{},
			wantFile: &creds.Profiles{Default: "work", Profiles: map[string]creds.Profile{"work": {
				Provider: "openrouter", Store: creds.StoreKeychain, Account: "work@{hash}", KeyEnd: "5678",
				Model: "typesafe/jev",
			}}},
			wantItems: map[string]string{"work@{hash}": "SECRET-OLD"},
		},
		{
			name:        "should replace the key and remove an old item no other profile names",
			profiles:    inKeychain("old"),
			withKey:     true,
			backend:     newItemBackend(map[string]string{"old": "SECRET-OLD"}),
			wantNotices: []creds.Notice{{Kind: creds.KeyInKeychain, Account: "work@{hash}"}},
			wantFile:    replaced,
			wantItems:   map[string]string{"work@{hash}": newKey},
		},
		{
			name:     "should name the old item left behind when its removal fails",
			profiles: inKeychain("old"),
			withKey:  true,
			backend:  &itemBackend{items: map[string]string{"old": "SECRET-OLD"}, deleteErr: locked},
			wantNotices: []creds.Notice{
				{Kind: creds.KeyInKeychain, Account: "work@{hash}"},
				{
					Kind: creds.ItemLeftBehind, Account: "old",
					Err: &creds.KeychainError{Op: "remove the key from", Err: locked},
				},
			},
			wantFile:  replaced,
			wantItems: map[string]string{"old": "SECRET-OLD", "work@{hash}": newKey},
		},
		{
			name:      "should put the old key back when the file cannot be written",
			profiles:  inKeychain("work@{hash}"),
			withKey:   true,
			backend:   newItemBackend(map[string]string{"work@{hash}": "SECRET-OLD"}),
			failWrite: true,
			wantErr:   errDiskFull,
			wantItems: map[string]string{"work@{hash}": "SECRET-OLD"},
		},
		{
			name:        "should say the old key was not read when the file cannot be written",
			profiles:    inKeychain("work@{hash}"),
			withKey:     true,
			backend:     &itemBackend{items: map[string]string{"work@{hash}": "SECRET-OLD"}, getErr: locked},
			failWrite:   true,
			wantErr:     errDiskFull,
			wantNotices: []creds.Notice{{Kind: creds.OldKeyUnread, Account: "work@{hash}"}},
			wantItems:   map[string]string{"work@{hash}": newKey},
		},
		{
			name:     "should say the old key was lost when putting it back fails",
			profiles: inKeychain("work@{hash}"),
			withKey:  true,
			backend: &itemBackend{
				items: map[string]string{"work@{hash}": "SECRET-OLD"}, setErrs: []error{nil, locked},
			},
			failWrite: true,
			wantErr:   errDiskFull,
			wantNotices: []creds.Notice{{
				Kind: creds.OldKeyLost, Account: "work@{hash}",
				Err: &creds.KeychainError{Op: "store the key in", Err: locked},
			}},
			wantItems: map[string]string{"work@{hash}": newKey},
		},
		{
			name:      "should fail before storing when the old key times out",
			profiles:  inKeychain("work@{hash}"),
			withKey:   true,
			backend:   &itemBackend{items: map[string]string{"work@{hash}": "SECRET-OLD"}, getErr: creds.ErrKeychainTimeout},
			wantErr:   creds.ErrKeychainTimeout,
			wantItems: map[string]string{"work@{hash}": "SECRET-OLD"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path, fill := profilesPath(t)
			profiles := filledProfiles(tc.profiles, fill)

			change := creds.Change{Path: path, Profiles: profiles, Name: "work", Profile: profiles.Profiles["work"]}
			change.Profile.Model = tc.model

			if tc.withKey {
				change.ReadKey = func() (string, error) { return newKey, nil }
			}

			tc.backend.fill(fill)

			notices, err := newManager(tc.backend, tc.failWrite).Update(change)

			checkChange(t, path, fill, notices, err, changeWant{
				err: tc.wantErr, notices: tc.wantNotices, file: tc.wantFile, items: tc.wantItems, backend: tc.backend,
			})
		})
	}
}

func TestProfileManagerDelete(t *testing.T) {
	t.Parallel()

	locked := errors.New("the keychain is locked")
	two := creds.Profiles{Default: "work", Profiles: map[string]creds.Profile{
		"work": {Provider: "openrouter", Store: creds.StoreKeychain, Account: "work@{hash}"},
		"home": {Provider: "typesafe", APIKey: "SECRET-HOME"},
	}}
	shared := creds.Profiles{Profiles: map[string]creds.Profile{
		"work": {Provider: "openrouter", Store: creds.StoreKeychain, Account: "shared"},
		"home": {Provider: "openrouter", Store: creds.StoreKeychain, Account: "shared"},
	}}

	tests := []struct {
		name        string
		profiles    creds.Profiles
		deleted     string
		backend     *itemBackend
		failWrite   bool
		wantErr     error
		wantNotices []creds.Notice
		wantFile    *creds.Profiles
		wantNoFile  bool
		wantItems   map[string]string
	}{
		{
			name:        "should remove the profile and its item, and say the default is gone",
			profiles:    two,
			deleted:     "work",
			backend:     newItemBackend(map[string]string{"work@{hash}": "SECRET-WORK"}),
			wantNotices: []creds.Notice{{Kind: creds.DefaultDeleted}},
			wantFile: &creds.Profiles{Profiles: map[string]creds.Profile{
				"home": {Provider: "typesafe", APIKey: "SECRET-HOME"},
			}},
			wantItems: map[string]string{},
		},
		{
			name:        "should keep an item another profile names",
			profiles:    shared,
			deleted:     "work",
			backend:     newItemBackend(map[string]string{"shared": "SECRET-SHARED"}),
			wantNotices: []creds.Notice{},
			wantFile: &creds.Profiles{Profiles: map[string]creds.Profile{
				"home": {Provider: "openrouter", Store: creds.StoreKeychain, Account: "shared"},
			}},
			wantItems: map[string]string{"shared": "SECRET-SHARED"},
		},
		{
			name:        "should remove the file with the last profile",
			profiles:    homeOnly(),
			deleted:     "home",
			backend:     newItemBackend(nil),
			wantNotices: []creds.Notice{},
			wantNoFile:  true,
			wantItems:   map[string]string{},
		},
		{
			name:     "should name the item left behind when its removal fails",
			profiles: two,
			deleted:  "work",
			backend:  &itemBackend{items: map[string]string{"work@{hash}": "SECRET-WORK"}, deleteErr: locked},
			wantNotices: []creds.Notice{
				{Kind: creds.DefaultDeleted},
				{
					Kind: creds.ItemLeftBehind, Account: "work@{hash}",
					Err: &creds.KeychainError{Op: "remove the key from", Err: locked},
				},
			},
			wantFile: &creds.Profiles{Profiles: map[string]creds.Profile{
				"home": {Provider: "typesafe", APIKey: "SECRET-HOME"},
			}},
			wantItems: map[string]string{"work@{hash}": "SECRET-WORK"},
		},
		{
			name:      "should keep the item when the file cannot be written",
			profiles:  two,
			deleted:   "work",
			backend:   newItemBackend(map[string]string{"work@{hash}": "SECRET-WORK"}),
			failWrite: true,
			wantErr:   errDiskFull,
			wantItems: map[string]string{"work@{hash}": "SECRET-WORK"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path, fill := profilesPath(t)
			profiles := filledProfiles(tc.profiles, fill)

			if _, err := creds.NewStore().SaveProfiles(path, profiles); err != nil {
				t.Fatalf("writing the profiles: %v", err)
			}

			tc.backend.fill(fill)

			notices, err := newManager(tc.backend, tc.failWrite).Delete(creds.Change{
				Path: path, Profiles: profiles, Name: tc.deleted,
			})

			want := changeWant{
				err: tc.wantErr, notices: tc.wantNotices, file: tc.wantFile, items: tc.wantItems, backend: tc.backend,
			}
			if tc.failWrite {
				want.file = &profiles
			}

			checkChange(t, path, fill, notices, err, want)

			if _, statErr := os.Stat(path); tc.wantNoFile && !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("profiles.json is there, stat error = %v", statErr)
			}
		})
	}
}

func TestProfilesWith(t *testing.T) {
	t.Parallel()

	t.Run("should add the profile to a copy and leave the original as it was", func(t *testing.T) {
		t.Parallel()

		original := homeOnly()
		changed := original.With("work", creds.Profile{Provider: "berget", APIKey: "SECRET-WORK"})

		if len(original.Profiles) != 1 || len(changed.Profiles) != 2 || changed.Default != original.Default {
			t.Errorf("With gave %v from %v, want a copy with one more profile", changed, original)
		}
	})

	t.Run("should make the map when the profiles have none", func(t *testing.T) {
		t.Parallel()

		changed := creds.Profiles{}.With("work", creds.Profile{Provider: "berget"})

		if changed.Profiles["work"].Provider != "berget" {
			t.Errorf("With gave %v, want the work profile", changed)
		}
	})
}

func homeOnly() creds.Profiles {
	return creds.Profiles{Default: "home", Profiles: map[string]creds.Profile{
		"home": {Provider: "typesafe", APIKey: "SECRET-HOME"},
	}}
}

func profilesPath(t *testing.T) (string, func(string) string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "profiles.json")

	return path, strings.NewReplacer(
		"{hash}", strings.TrimPrefix(creds.KeychainAccount("x", path), "x@"),
		"{path}", path,
	).Replace
}

func newManager(backend *itemBackend, failWrite bool) *creds.ProfileManager {
	var opts []creds.StoreOption
	if failWrite {
		opts = append(opts, creds.WithCreateTemp(func(string, string) (*os.File, error) { return nil, errDiskFull }))
	}

	return creds.NewProfileManager(creds.NewStore(opts...), creds.NewKeychain(creds.WithBackend(backend)))
}

func checkChange(
	t *testing.T, path string, fill func(string) string, notices []creds.Notice, err error, want changeWant,
) {
	t.Helper()

	switch {
	case want.err == nil && err != nil:
		t.Fatalf("error = %v, want none", err)
	case want.err != nil && !errors.Is(err, want.err) && fill(want.err.Error()) != errText(err):
		t.Fatalf("error = %v, want %v", err, want.err)
	}

	wantNotices := make([]creds.Notice, 0, len(want.notices))
	for _, notice := range want.notices {
		notice.Account = fill(notice.Account)
		wantNotices = append(wantNotices, notice)
	}

	if got, wanted := shown(notices), shown(wantNotices); !slices.Equal(got, wanted) {
		t.Errorf("notices = %+v, want %+v", got, wanted)
	}

	if want.file != nil {
		assertSaved(t, path, filledProfiles(*want.file, fill))
	}

	if items := want.backend.contents(); want.items != nil && !maps.Equal(items, filledItems(want.items, fill)) {
		t.Errorf("keychain items %v, want %v", keysOf(items), keysOf(filledItems(want.items, fill)))
	}
}

type changeWant struct {
	err     error
	notices []creds.Notice
	file    *creds.Profiles
	items   map[string]string
	backend *itemBackend
}

func filledProfiles(profiles creds.Profiles, fill func(string) string) creds.Profiles {
	filled := creds.Profiles{Default: profiles.Default, Profiles: map[string]creds.Profile{}}
	for name, profile := range profiles.Profiles {
		profile.Account = fill(profile.Account)
		filled.Profiles[name] = profile
	}

	return filled
}

func shown(notices []creds.Notice) []shownNotice {
	lines := make([]shownNotice, 0, len(notices))
	for _, notice := range notices {
		lines = append(lines, shownNotice{
			kind: notice.Kind, account: notice.Account, err: errText(notice.Err),
		})
	}

	return lines
}

type shownNotice struct {
	kind         creds.NoticeKind
	account, err string
}

func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

func assertSaved(t *testing.T, path string, want creds.Profiles) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading profiles.json: %v", err)
	}

	var got creds.Profiles
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decoding profiles.json: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("profiles.json = %+v, want %+v", got, want)
	}
}

func keysOf(items map[string]string) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}

	return keys
}

func newItemBackend(items map[string]string) *itemBackend {
	if items == nil {
		items = map[string]string{}
	}

	return &itemBackend{items: items}
}

type itemBackend struct {
	mu        sync.Mutex
	items     map[string]string
	getErr    error
	setErrs   []error
	sets      int
	deleteErr error
}

func (b *itemBackend) fill(fill func(string) string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.items = filledItems(b.items, fill)
}

func (b *itemBackend) Get(_, user string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.getErr != nil {
		return "", b.getErr
	}

	key, ok := b.items[user]
	if !ok {
		return "", creds.ErrKeychainMissing
	}

	return key, nil
}

func (b *itemBackend) Set(_, user, password string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.sets++
	if b.sets <= len(b.setErrs) && b.setErrs[b.sets-1] != nil {
		return b.setErrs[b.sets-1]
	}

	b.items[user] = password

	return nil
}

func (b *itemBackend) Delete(_, user string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.deleteErr != nil {
		return b.deleteErr
	}

	delete(b.items, user)

	return nil
}

func (b *itemBackend) contents() map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return maps.Clone(b.items)
}

func filledItems(items map[string]string, fill func(string) string) map[string]string {
	filled := make(map[string]string, len(items))
	for account, key := range items {
		filled[fill(account)] = key
	}

	return filled
}
