package dinner

import (
	"fmt"
	"strings"

	"example.com/whatfordinner/internal/catalog"
)

// tagGroup 是配置文件中的一层标签；四层顺序固定为大类、菜系、品类、就餐形式。
type tagGroup struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

var tagGroupOrder = []string{"大类", "菜系", "品类", "就餐形式"}

// tagTaxonomy 是插件启动时校验并固定下来的标签词表。
type tagTaxonomy struct {
	groups []tagGroup
	names  map[string]bool
}

func newTagTaxonomy(groups []tagGroup) (*tagTaxonomy, error) {
	known := make(map[string]bool, len(tagGroupOrder))
	for _, name := range tagGroupOrder {
		known[name] = true
	}
	byName := make(map[string]tagGroup, len(groups))
	for _, group := range groups {
		name := strings.TrimSpace(group.Name)
		if name == "" {
			return nil, fmt.Errorf("标签层名称不能为空")
		}
		if !known[name] {
			return nil, fmt.Errorf("未知标签层 %q", name)
		}
		if _, exists := byName[name]; exists {
			return nil, fmt.Errorf("标签层 %q 重复", name)
		}
		byName[name] = group
	}

	taxonomy := &tagTaxonomy{names: make(map[string]bool)}
	for _, name := range tagGroupOrder {
		group, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("标签配置缺少「%s」层", name)
		}
		normalized := tagGroup{Name: name}
		for _, tag := range group.Tags {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				return nil, fmt.Errorf("标签层「%s」包含空标签", name)
			}
			if strings.ContainsAny(tag, " \t\r\n") {
				return nil, fmt.Errorf("标签 %q 不能包含空白字符", tag)
			}
			if taxonomy.names[tag] {
				return nil, fmt.Errorf("标签 %q 重复", tag)
			}
			taxonomy.names[tag] = true
			normalized.Tags = append(normalized.Tags, tag)
		}
		if len(normalized.Tags) == 0 {
			return nil, fmt.Errorf("标签层「%s」不能为空", name)
		}
		taxonomy.groups = append(taxonomy.groups, normalized)
	}
	return taxonomy, nil
}

// Has 报告标签是否在配置词表中，与当前餐厅是否使用无关。
func (t *tagTaxonomy) Has(tag string) bool {
	return t != nil && t.names[tag]
}

// allGroups 返回按固定层顺序排列的副本，供命令展示。
func (t *tagTaxonomy) allGroups() []tagGroup {
	if t == nil {
		return nil
	}
	groups := make([]tagGroup, len(t.groups))
	for i, group := range t.groups {
		groups[i] = tagGroup{Name: group.Name, Tags: append([]string(nil), group.Tags...)}
	}
	return groups
}

// validateOptions 保证清单中出现的每个标签都已写进标签配置。
func (t *tagTaxonomy) validateOptions(options []catalog.Option) error {
	for _, option := range options {
		for _, tag := range option.Tags {
			if !t.Has(tag) {
				return fmt.Errorf("catalog item %q 使用未配置的标签 %q", option.ID, tag)
			}
		}
	}
	return nil
}
