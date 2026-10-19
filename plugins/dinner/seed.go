package dinner

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"example.com/whatfordinner/internal/catalog"
	"gopkg.in/yaml.v3"
)

// catalogSeed 是首次接入统一存储时使用的餐厅清单文件格式。
type catalogSeed struct {
	Items []catalog.Option `yaml:"items"`
}

// loadCatalogSeed 读取并解析独立的餐厅 seed 文件，不负责写入存储。
func loadCatalogSeed(path string) ([]catalog.Option, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("dinner: catalog_seed 不能为空")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("dinner: 读取 catalog_seed %q: %w", path, err)
	}
	var seed catalogSeed
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&seed); err != nil {
		return nil, fmt.Errorf("dinner: 解析 catalog_seed %q: %w", path, err)
	}
	return seed.Items, nil
}
