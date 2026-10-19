package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RandomLemon/kei/pkg/bot"
)

type fakeStorage struct {
	values   map[string][]byte
	getErr   error
	setErr   error
	setKey   string
	setTTL   time.Duration
	setValue []byte
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{values: make(map[string][]byte)}
}

func (s *fakeStorage) Get(_ context.Context, key string) ([]byte, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	value, ok := s.values[key]
	if !ok {
		return nil, bot.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *fakeStorage) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	if s.setErr != nil {
		return s.setErr
	}
	s.setKey = key
	s.setTTL = ttl
	s.setValue = append([]byte(nil), value...)
	s.values[key] = append([]byte(nil), value...)
	return nil
}

func (s *fakeStorage) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func testItems() []Option {
	price := int64(1800)
	return []Option{
		{
			ID:         "a",
			Name:       "A",
			Venue:      "测试地址",
			PriceCents: &price,
			Tags:       []string{"火锅", "点餐"},
			Enabled:    true,
		},
	}
}

func TestRepositoryLoadNotFound(t *testing.T) {
	storage := newFakeStorage()
	items, err := NewRepository(storage).Load(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load error = %v, want ErrNotFound", err)
	}
	if items != nil {
		t.Fatalf("Load items = %#v, want nil", items)
	}
	if storage.setKey != "" {
		t.Fatal("Load must not write storage")
	}
}

func TestRepositorySaveAndLoad(t *testing.T) {
	storage := newFakeStorage()
	repository := NewRepository(storage)
	items := testItems()

	if err := repository.Save(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	if storage.setKey != catalogKey {
		t.Fatalf("Set key = %q, want %q", storage.setKey, catalogKey)
	}
	if storage.setTTL != 0 {
		t.Fatalf("Set TTL = %v, want 0", storage.setTTL)
	}
	var document catalogDocument
	if err := json.Unmarshal(storage.setValue, &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != catalogVersion {
		t.Fatalf("version = %d, want %d", document.Version, catalogVersion)
	}
	if !reflect.DeepEqual(document.Items, items) {
		t.Fatalf("stored items = %#v, want %#v", document.Items, items)
	}

	storage.setKey = ""
	loaded, err := repository.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, items) {
		t.Fatalf("loaded items = %#v, want %#v", loaded, items)
	}
	if storage.setKey != "" {
		t.Fatal("Load must not write storage")
	}
}

func TestRepositorySaveStorageFailure(t *testing.T) {
	failure := errors.New("set failure")
	storage := newFakeStorage()
	storage.setErr = failure
	err := NewRepository(storage).Save(context.Background(), testItems())
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "save dinner catalog") {
		t.Fatalf("Save error = %v, want storage failure with prefix", err)
	}
	if len(storage.values) != 0 {
		t.Fatal("failed Save must not write catalog")
	}
}

func TestRepositoryJSONContract(t *testing.T) {
	storage := newFakeStorage()
	if err := NewRepository(storage).Save(context.Background(), testItems()); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Version int                          `json:"version"`
		Items   []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(storage.setValue, &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != 1 {
		t.Fatalf("version = %d, want 1", document.Version)
	}
	if len(document.Items) != 1 {
		t.Fatalf("items length = %d, want 1", len(document.Items))
	}
	for _, key := range []string{"id", "name", "venue", "price_cents", "tags", "enabled"} {
		if _, ok := document.Items[0][key]; !ok {
			t.Fatalf("missing JSON field %q in %s", key, storage.setValue)
		}
	}
}

func TestRepositorySaveNormalizes(t *testing.T) {
	storage := newFakeStorage()
	repository := NewRepository(storage)
	items := []Option{{ID: "a", Name: "A", Tags: []string{" 火锅 ", "火锅"}, Enabled: true}}
	if err := repository.Save(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || !reflect.DeepEqual(loaded[0].Tags, []string{"火锅"}) {
		t.Fatalf("loaded tags = %#v, want [火锅]", loaded)
	}
}

func TestRepositorySaveValidates(t *testing.T) {
	negativePrice := int64(-1)
	cases := []struct {
		name  string
		items []Option
		want  string
	}{
		{
			name:  "empty id",
			items: []Option{{Name: "A"}},
			want:  "id 和 name 不能为空",
		},
		{
			name:  "empty name",
			items: []Option{{ID: "a"}},
			want:  "id 和 name 不能为空",
		},
		{
			name:  "duplicate id",
			items: []Option{{ID: "a", Name: "A"}, {ID: "a", Name: "B"}},
			want:  "重复",
		},
		{
			name:  "negative price",
			items: []Option{{ID: "a", Name: "A", PriceCents: &negativePrice}},
			want:  "不能为负",
		},
		{
			name:  "empty tag",
			items: []Option{{ID: "a", Name: "A", Tags: []string{" "}}},
			want:  "标签不能为空",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storage := newFakeStorage()
			err := NewRepository(storage).Save(context.Background(), tc.items)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Save error = %v, want containing %q", err, tc.want)
			}
			if len(storage.values) != 0 {
				t.Fatal("invalid catalog must not be written")
			}
		})
	}
}

func TestRepositoryLoadErrors(t *testing.T) {
	getFailure := errors.New("storage failure")
	corrupt := []byte("{")
	unknownVersion := []byte(`{"version":2,"items":[]}`)
	missingVersion := []byte(`{"items":[]}`)
	cases := []struct {
		name    string
		setup   func(*fakeStorage)
		want    string
		wantErr error
	}{
		{
			name:    "storage error",
			setup:   func(s *fakeStorage) { s.getErr = getFailure },
			want:    "load dinner catalog",
			wantErr: getFailure,
		},
		{
			name:  "corrupt json",
			setup: func(s *fakeStorage) { s.values[catalogKey] = corrupt },
			want:  "decode",
		},
		{
			name:  "unknown version",
			setup: func(s *fakeStorage) { s.values[catalogKey] = unknownVersion },
			want:  "不支持的版本 2",
		},
		{
			name:  "missing version",
			setup: func(s *fakeStorage) { s.values[catalogKey] = missingVersion },
			want:  "不支持的版本 0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storage := newFakeStorage()
			tc.setup(storage)
			_, err := NewRepository(storage).Load(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v, want containing %q", err, tc.want)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("Load error = %v, want wrapping %v", err, tc.wantErr)
			}
		})
	}
}

func TestRepositoryEmptyCatalog(t *testing.T) {
	storage := newFakeStorage()
	repository := NewRepository(storage)
	if err := repository.Save(context.Background(), []Option{}); err != nil {
		t.Fatal(err)
	}
	items, err := repository.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("loaded items = %#v, want empty", items)
	}
}

func TestRepositoryStorageRequired(t *testing.T) {
	repository := NewRepository(nil)
	if _, err := repository.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "storage 未配置") {
		t.Fatalf("Load error = %v, want storage 未配置", err)
	}
	if err := repository.Save(context.Background(), testItems()); err == nil || !strings.Contains(err.Error(), "storage 未配置") {
		t.Fatalf("Save error = %v, want storage 未配置", err)
	}
}

func TestRepositoryMapsWrappedNotFound(t *testing.T) {
	storage := newFakeStorage()
	storage.getErr = fmt.Errorf("wrapped: %w", bot.ErrNotFound)
	_, err := NewRepository(storage).Load(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load error = %v, want ErrNotFound", err)
	}
}
