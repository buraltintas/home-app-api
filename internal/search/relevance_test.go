package search

import "testing"

func TestCategoryLedSearchRejectsProviderFuzzOutsideTheRequestedTrade(t *testing.T) {
	if categoriesIntersect([]string{"kitchenware"}, []string{"small_appliances"}) {
		t.Error("a kitchen showroom became a small-appliance result without that category")
	}
	if categoriesIntersect([]string{"major_appliances", "small_appliances"}, []string{"small_appliances"}) == false {
		t.Error("a white-goods dealer should answer a small-appliance search")
	}
	if categoriesIntersect([]string{"home_accessories", "small_appliances"}, []string{"major_appliances"}) {
		t.Error("a small-appliance shop should not answer a white-goods search")
	}
}
