package catalog

import (
	"strings"
	"testing"
)

func TestCatalogValidation(t *testing.T) {
	negative := int64(-1)
	for name, items := range map[string][]Option{
		"duplicate":  {{ID: "a", Name: "A"}, {ID: "a", Name: "B"}},
		"empty name": {{ID: "a"}}, "empty id": {{Name: "A"}},
		"whitespace id": {{ID: "a b", Name: "A"}},
		"negative":      {{ID: "a", Name: "A", PriceCents: &negative}},
		"empty tag":     {{ID: "a", Name: "A", Tags: []string{" "}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(items)
			if err == nil || !strings.Contains(err.Error(), "catalog.items[") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := New(nil); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogReadOnlyAndOrder(t *testing.T) {
	price := int64(1800)
	input := []Option{{ID: "b", Name: "B", PriceCents: &price, Tags: []string{" 面食 ", "面食"}}, {ID: "a", Name: "A"}}
	c, err := New(input)
	if err != nil {
		t.Fatal(err)
	}
	price = 9000
	input[0].Tags[0] = "changed"
	input[0].Name = "changed"
	list := c.List()
	if list[0].ID != "b" || list[1].ID != "a" || len(list[0].Tags) != 1 {
		t.Fatalf("list = %+v", list)
	}
	*list[0].PriceCents = 1
	list[0].Tags[0] = "changed"
	item, ok := c.Find("b")
	if !ok || item.Name != "B" || *item.PriceCents != 1800 || item.Tags[0] != "面食" {
		t.Fatalf("item = %+v", item)
	}
	*item.PriceCents = 2
	item.Tags[0] = "changed"
	again, _ := c.Find("b")
	if *again.PriceCents != 1800 || again.Tags[0] != "面食" {
		t.Fatalf("mutable find: %+v", again)
	}
	if _, ok := c.Find("missing"); ok {
		t.Fatal("unexpected match")
	}
}
