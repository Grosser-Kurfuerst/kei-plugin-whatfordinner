package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/RandomLemon/kei/pkg/bot"
)

const (
	catalogKey     = "dinner:catalog"
	catalogVersion = 1
)

// ErrNotFound 表示统一存储中还没有导入餐厅清单。
var ErrNotFound = errors.New("catalog: not found")

// Repository 把餐厅清单保存为统一存储中的一个 JSON 文档。
type Repository struct {
	storage bot.Storage
}

// NewRepository 使用宿主提供的存储，不自行打开数据库。
func NewRepository(storage bot.Storage) *Repository {
	return &Repository{storage: storage}
}

type catalogDocument struct {
	Version int      `json:"version"`
	Items   []Option `json:"items"`
}

// Load 读取并解码清单；键不存在时返回 ErrNotFound。
func (r *Repository) Load(ctx context.Context) ([]Option, error) {
	if r == nil || r.storage == nil {
		return nil, fmt.Errorf("load dinner catalog: storage 未配置")
	}
	data, err := r.storage.Get(ctx, catalogKey)
	if errors.Is(err, bot.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load dinner catalog: %w", err)
	}
	var document catalogDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("load dinner catalog: decode: %w", err)
	}
	if document.Version != catalogVersion {
		return nil, fmt.Errorf("load dinner catalog: 不支持的版本 %d", document.Version)
	}
	return document.Items, nil
}

// Save 校验并覆盖保存完整清单，TTL=0 表示不过期。
func (r *Repository) Save(ctx context.Context, items []Option) error {
	if r == nil || r.storage == nil {
		return fmt.Errorf("save dinner catalog: storage 未配置")
	}
	c, err := New(items)
	if err != nil {
		return fmt.Errorf("save dinner catalog: %w", err)
	}
	data, err := json.Marshal(catalogDocument{Version: catalogVersion, Items: c.List()})
	if err != nil {
		return fmt.Errorf("save dinner catalog: encode: %w", err)
	}
	if err := r.storage.Set(ctx, catalogKey, data, 0); err != nil {
		return fmt.Errorf("save dinner catalog: %w", err)
	}
	return nil
}
