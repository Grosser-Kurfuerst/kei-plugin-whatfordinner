package dinner

import (
	"strings"
	"testing"

	"example.com/whatfordinner/internal/catalog"
)

func testTagGroups() []tagGroup {
	return []tagGroup{
		{Name: "大类", Tags: []string{"中餐", "西餐", "亚洲菜", "其他"}},
		{Name: "菜系", Tags: []string{"川菜", "淮扬菜", "韩国料理", "浙江菜"}},
		{Name: "品类", Tags: []string{"火锅", "面食", "米饭", "炸鸡", "烤肉", "披萨"}},
		{Name: "就餐形式", Tags: []string{"自助餐", "点餐"}},
	}
}

func TestTagTaxonomy(t *testing.T) {
	taxonomy, err := newTagTaxonomy(testTagGroups())
	if err != nil {
		t.Fatal(err)
	}
	if !taxonomy.Has("川菜") || taxonomy.Has("不存在") {
		t.Fatal("Has returned unexpected result")
	}
	groups := taxonomy.allGroups()
	if len(groups) != 4 || groups[0].Name != "大类" || groups[3].Name != "就餐形式" {
		t.Fatalf("groups = %+v", groups)
	}
	groups[0].Tags[0] = "changed"
	if taxonomy.Has("changed") || !taxonomy.Has("中餐") {
		t.Fatal("allGroups did not return a copy")
	}

	reversed := []tagGroup{testTagGroups()[3], testTagGroups()[2], testTagGroups()[1], testTagGroups()[0]}
	normalized, err := newTagTaxonomy(reversed)
	if err != nil || normalized.allGroups()[0].Name != "大类" {
		t.Fatalf("layer order not normalized: %+v %v", normalized, err)
	}
}

func TestTagTaxonomyValidation(t *testing.T) {
	cases := map[string][]tagGroup{}
	missing := testTagGroups()[1:]
	cases["missing group"] = missing
	cases["unknown group"] = append(testTagGroups(), tagGroup{Name: "额外", Tags: []string{"其他"}})
	cases["duplicate group"] = append(testTagGroups(), tagGroup{Name: "大类", Tags: []string{"其他"}})
	emptyGroup := testTagGroups()
	emptyGroup[0].Tags = nil
	cases["empty group"] = emptyGroup
	emptyTag := testTagGroups()
	emptyTag[0].Tags = []string{" "}
	cases["empty tag"] = emptyTag
	whitespaceTag := testTagGroups()
	whitespaceTag[0].Tags = []string{"中 餐"}
	cases["whitespace tag"] = whitespaceTag
	duplicateTag := testTagGroups()
	duplicateTag[1].Tags = append(duplicateTag[1].Tags, "中餐")
	cases["duplicate tag"] = duplicateTag

	for name, groups := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := newTagTaxonomy(groups); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestTagTaxonomyValidatesCatalog(t *testing.T) {
	taxonomy, err := newTagTaxonomy(testTagGroups())
	if err != nil {
		t.Fatal(err)
	}
	if err := taxonomy.validateOptions([]catalog.Option{{ID: "a", Tags: []string{"川菜", "点餐"}}}); err != nil {
		t.Fatal(err)
	}
	err = taxonomy.validateOptions([]catalog.Option{{ID: "a", Tags: []string{"不存在"}}})
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("error = %v", err)
	}
}
